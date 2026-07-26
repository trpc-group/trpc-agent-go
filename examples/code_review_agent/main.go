// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// # GoLens - 基于 trpc-agent-go 的自动代码评审 Agent 示例
//
// 本示例展示了如何使用 trpc-agent-go 框架构建一个自动代码评审系统，
// 包含 CR Skill、沙箱执行、PermissionPolicy、数据库存储等功能。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	fake "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/model"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	security "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

var (
	flagModel      = flag.String("model", "", "LLM model name (default: from env or hy3)")
	flagDiffFile   = flag.String("diff-file", "", "Diff file to review")
	flagRepoPath   = flag.String("repo-path", ".", "Repository path")
	flagOutput     = flag.String("output", "json", "Output format: json|md|text")
	flagDB         = flag.String("db", "golens.db", "Database path")
	flagDryRun     = flag.Bool("dry-run", false, "Dry run mode (rules only, no sandbox)")
	flagFakeModel  = flag.Bool("fake-model", false, "Use fake model for testing (no real LLM needed)")
	flagSkillsRoot = flag.String("skills-root", "", "Skills root directory")
	flagExec       = flag.String("executor", "local", "Code executor: local|container|e2b")
)

const (
	appName      = "golens"
	defaultModel = "hy3"
	skillsDir    = "skills"
)

func main() {
	flag.Parse()

	if *flagDiffFile == "" {
		fmt.Println("Error: --diff-file is required")
		flag.Usage()
		os.Exit(1)
	}

	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run() error {
	// 创建带超时的 context（2 分钟）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	startTime := time.Now()

	// 1. 初始化数据库
	db, err := store.InitDB(*flagDB)
	if err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	defer db.Close()

	// 2. 创建审查任务
	taskID := store.GenerateTaskID()
	task := &store.ReviewTask{
		TaskID:    taskID,
		RepoPath:  *flagRepoPath,
		DiffFile:  *flagDiffFile,
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if err := store.SaveTask(db, task); err != nil {
		return fmt.Errorf("save task: %w", err)
	}

	// 3. 解析输入（支持多种输入方式）
	parser := input.NewDiffParser(*flagRepoPath)
	var diffResult *input.DiffParseResult

	if *flagDiffFile != "" {
		// 方式1: 解析 diff 文件
		diffResult, err = parser.ParseFile(*flagDiffFile)
	} else {
		// 方式2: 解析 git 工作区变更
		diffResult, err = parser.ParseGitDiff(ctx)
	}

	if err != nil {
		store.UpdateTaskStatus(db, taskID, "failed", err.Error())
		return fmt.Errorf("parse diff: %w", err)
	}

	// 保存 diff 摘要
	for _, file := range diffResult.Files {
		if err := store.SaveDiffSummary(db, taskID, file.Path, file.Status, file.Additions, file.Deletions); err != nil {
			log.Printf("Warning: failed to save diff summary for %s: %v", file.Path, err)
		}
	}

	// 4. 初始化规则引擎
	ruleEngine := rules.NewRuleEngine()

	// 5. 应用规则
	fmt.Printf("Applying rules...\n")
	findings := ruleEngine.CheckDiffResult(taskID, diffResult)

	// 5.5 AI 增强分析
	if *flagFakeModel {
		// 使用 fake model（不需要真实 LLM）
		fmt.Printf("Running fake model analysis...\n")
		fakeModel := fake.NewFakeModel("fake-gpt")
		diffContent := readDiffContent(*flagDiffFile)
		aiResponse, err := fakeModel.GenerateResponse(ctx, diffContent)
		if err != nil {
			log.Printf("Warning: fake model analysis failed: %v", err)
		} else {
			aiFindings := parseAIFindings(aiResponse, taskID)
			findings = append(findings, aiFindings...)
		}
	} else if os.Getenv("OPENAI_API_KEY") != "" {
		// 使用真实 LLM
		fmt.Printf("Running LLM analysis (model: %s)...\n", getModelName())
		llmFindings, err := runLLMAnalysis(ctx, taskID, diffResult)
		if err != nil {
			log.Printf("Warning: LLM analysis failed: %v", err)
		} else {
			findings = append(findings, llmFindings...)
		}
	}

	// 6. 去重降噪
	uniqueFindings, warnings := rules.DeduplicateFindings(findings)

	// 7. Permission policy check
	permPolicy := security.NewPermissionPolicy()
	secretDetector := security.NewSecretDetector()

	// Record initial permission decision
	permDecision := permPolicy.Evaluate("review")
	if err := store.SavePermissionDecision(db, &store.PermissionDecision{
		TaskID:   taskID,
		Command:  "review",
		Decision: string(permDecision.Decision),
		Reason:   permDecision.Reason,
	}); err != nil {
		log.Printf("Warning: failed to save permission decision: %v", err)
	}

	// 8. 沙箱执行（非 dry-run 模式）
	var sandboxRuns []store.SandboxRun
	if !*flagDryRun {
		fmt.Printf("Running sandbox checks (executor: %s)...\n", *flagExec)

		// 沙箱执行前检查权限
		deniedCommands := make(map[string]bool)
		for _, script := range []string{"go vet", "staticcheck"} {
			decision := permPolicy.Evaluate(script)
			if err := store.SavePermissionDecision(db, &store.PermissionDecision{
				TaskID:   taskID,
				Command:  script,
				Decision: string(decision.Decision),
				Reason:   decision.Reason,
			}); err != nil {
				log.Printf("Warning: failed to save permission decision: %v", err)
			}
			if decision.Decision == "deny" {
				deniedCommands[script] = true
			}
		}

		// Only execute if commands are not denied
		if !deniedCommands["go vet"] && !deniedCommands["staticcheck"] {
			// Create sandbox executor based on type
			var sandboxExec *sandbox.Executor
			switch *flagExec {
			case "container":
				sandboxExec = sandbox.NewExecutorWithType(*flagRepoPath, sandbox.ExecutorContainer)
			case "e2b":
				sandboxExec = sandbox.NewExecutorWithType(*flagRepoPath, sandbox.ExecutorE2B)
			default:
				sandboxExec = sandbox.NewExecutor(*flagRepoPath)
			}

			runs, err := sandboxExec.RunAllChecks(ctx, taskID)
			if err != nil {
				log.Printf("Warning: sandbox checks failed: %v", err)
			}
			sandboxRuns = runs

			// Save sandbox run records and check for failures
			sandboxFailed := false
			for _, run := range sandboxRuns {
				if err := store.SaveSandboxRun(db, &run); err != nil {
					log.Printf("Warning: failed to save sandbox run: %v", err)
				}
				if run.ExitCode != 0 {
					sandboxFailed = true
				}
			}

			// Mark task as failed if sandbox execution failed
			if sandboxFailed {
				store.UpdateTaskStatus(db, taskID, "failed", "sandbox execution failed")
			}
		} else {
			log.Printf("Warning: sandbox execution skipped due to permission denial")
		}
	}

	// 9. Sensitive information redaction
	for i := range uniqueFindings {
		if secretDetector.Detect(uniqueFindings[i].Evidence) {
			uniqueFindings[i].Evidence = "<redacted>"
		}
		if secretDetector.Detect(uniqueFindings[i].Description) {
			uniqueFindings[i].Description = secretDetector.RedactText(uniqueFindings[i].Description)
		}
		if secretDetector.Detect(uniqueFindings[i].Title) {
			uniqueFindings[i].Title = secretDetector.RedactText(uniqueFindings[i].Title)
		}
		if secretDetector.Detect(uniqueFindings[i].Recommendation) {
			uniqueFindings[i].Recommendation = secretDetector.RedactText(uniqueFindings[i].Recommendation)
		}
	}

	// 8. 保存 findings
	if err := store.SaveFindings(db, uniqueFindings); err != nil {
		store.UpdateTaskStatus(db, taskID, "failed", err.Error())
		return fmt.Errorf("save findings: %w", err)
	}

	// 9. 计算监控摘要
	monitoring := store.CalculateMonitoring(taskID, uniqueFindings, sandboxRuns, startTime)
	store.SaveMonitoringSummary(db, monitoring)

	// 10. 生成报告
	reviewReport := report.Generate(taskID, uniqueFindings, warnings, sandboxRuns, monitoring)

	// 11. 更新任务状态
	store.UpdateTaskStatus(db, taskID, "completed", "")

	// 12. 输出结果
	switch *flagOutput {
	case "json":
		report.PrintJSON(reviewReport)
	case "md":
		report.PrintMarkdown(reviewReport)
	case "text":
		report.PrintText(reviewReport)
	default:
		report.PrintJSON(reviewReport)
	}

	// 13. 保存报告文件
	report.SaveJSON(reviewReport, "review_report.json")
	report.SaveMarkdown(reviewReport, "review_report.md")

	// 14. 保存报告到数据库
	reportJSON, _ := json.MarshalIndent(reviewReport, "", "  ")
	reportMD := report.GenerateMarkdownString(reviewReport)
	store.SaveReviewReport(db, &store.ReviewReport{
		TaskID:     taskID,
		ReportJSON: string(reportJSON),
		ReportMD:   reportMD,
	})

	// 15. 保存 artifact
	store.SaveArtifact(db, &store.Artifact{
		TaskID:       taskID,
		ArtifactType: "report_json",
		FilePath:     "review_report.json",
		Content:      string(reportJSON),
	})

	fmt.Printf("\nReview completed: %s\n", taskID)
	fmt.Printf("Duration: %dms\n", time.Since(startTime).Milliseconds())
	fmt.Printf("Findings: %d (critical: %d, high: %d, medium: %d, low: %d)\n",
		reviewReport.Summary.TotalFindings,
		reviewReport.Summary.CriticalCount,
		reviewReport.Summary.HighCount,
		reviewReport.Summary.MediumCount,
		reviewReport.Summary.LowCount,
	)

	return nil
}

// getModelName 获取模型名称

// getModelName 获取模型名称
func getModelName() string {
	modelName := *flagModel
	if modelName == "" {
		modelName = os.Getenv("OPENAI_MODEL")
		if modelName == "" {
			modelName = defaultModel
		}
	}
	return modelName
}

// runLLMAnalysis 使用真实 LLM 进行分析
func runLLMAnalysis(ctx context.Context, taskID string, diffResult *input.DiffParseResult) ([]store.Finding, error) {
	// 构建 diff 内容
	var diffContent strings.Builder
	for _, file := range diffResult.Files {
		for _, hunk := range file.Hunks {
			for _, change := range hunk.Changes {
				prefix := " "
				switch change.Type {
				case "add":
					prefix = "+"
				case "delete":
					prefix = "-"
				}
				diffContent.WriteString(prefix)
				diffContent.WriteString(" ")
				diffContent.WriteString(change.Content)
				diffContent.WriteString("\n")
			}
		}
	}

	// 构建 prompt
	prompt := fmt.Sprintf(`请审查以下 Go 代码变更，找出潜在问题。

Diff:
%s

请以 JSON 格式返回审查结果，格式如下：
{
  "findings": [
    {
      "severity": "critical|high|medium|low|info",
      "category": "security|goroutine|resource|error|test|database",
      "file": "文件名",
      "line": 行号,
      "title": "问题标题",
      "description": "问题描述",
      "evidence": "代码证据",
      "recommendation": "修复建议",
      "confidence": 0.95,
      "source": "ai",
      "rule_id": "AI_XXX"
    }
  ]
}

只返回 JSON，不要返回其他内容。`, diffContent.String())

	// 使用 trpc-agent-go 框架调用 LLM
	modelName := getModelName()
	mdl := openai.New(modelName)

	genConfig := model.GenerationConfig{
		MaxTokens: intPtr(4000),
		Stream:    false,
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithDescription("Go 代码审查 Agent"),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(genConfig),
	}

	agent := llmagent.New("code-reviewer", agentOpts...)
	r := runner.NewRunner(appName, agent)

	// 执行
	events, err := r.Run(ctx, "user", "review-"+taskID, model.NewUserMessage(prompt))
	if err != nil {
		return nil, fmt.Errorf("LLM run failed: %w", err)
	}

	// 收集结果
	var response string
	eventCount := 0
	for event := range events {
		eventCount++
		if event.Error != nil {
			log.Printf("Event error: %v", event.Error)
			continue
		}
		if event.Response != nil {
			// 获取响应内容
			content := getResponseContent(event.Response)
			if content != "" {
				response = content
				log.Printf("Got LLM response (length: %d)", len(content))
			}
		}
	}

	log.Printf("Total events received: %d", eventCount)

	if response == "" {
		return nil, fmt.Errorf("no response from LLM")
	}

	// 解析 findings
	return parseAIFindings(response, taskID), nil
}

// getResponseContent 从 Response 中提取内容
func getResponseContent(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

// readDiffContent 读取 diff 文件内容
func readDiffContent(diffFile string) string {
	content, err := os.ReadFile(diffFile)
	if err != nil {
		return ""
	}
	return string(content)
}

// parseAIFindings parses findings from AI response
func parseAIFindings(response string, taskID string) []store.Finding {
	findings := make([]store.Finding, 0)

	// Strip markdown code fences if present
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Try to parse JSON
	var result struct {
		Findings []store.Finding `json:"findings"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		log.Printf("Warning: failed to parse AI findings JSON: %v", err)
		return findings
	}

	for _, f := range result.Findings {
		f.TaskID = taskID
		if f.Source == "" {
			f.Source = "ai"
		}
		findings = append(findings, f)
	}

	return findings
}

func intPtr(i int) *int {
	return &i
}

const instructionText = `你是一个专业的 Go 代码审查助手。你的职责是：

1. 分析代码变更（diff），识别潜在问题
2. 使用 CR Skill 中定义的规则进行审查
3. 在沙箱中执行 go vet、go test 等静态检查
4. 生成结构化的审查报告

审查规则覆盖以下类别：
- 安全风险（SQL 注入、敏感信息泄漏）
- Goroutine/Context 泄漏
- 资源未关闭（文件、连接）
- 错误处理不当
- 测试缺失
- 数据库事务问题

输出格式要求：
- 严重级别：critical、high、medium、low、info
- 每个 finding 包含：severity、category、file、line、title、evidence、recommendation、confidence、source、rule_id
- 同一文件同一行同一类问题不能重复报
- 低置信度问题（<0.7）应降级为 warnings`
