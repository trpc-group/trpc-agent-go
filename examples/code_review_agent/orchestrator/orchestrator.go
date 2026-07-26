// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package orchestrator 提供端到端审查流程编排
package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/model"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	security "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// ReviewMode 审查模式
type ReviewMode string

const (
	ModeDryRun    ReviewMode = "dry-run"    // 只用规则，不执行沙箱
	ModeFakeModel ReviewMode = "fake-model" // 使用 fake model
	ModeLLM       ReviewMode = "llm"        // 使用真实 LLM
)

// ReviewConfig 审查配置
type ReviewConfig struct {
	TaskID   string
	RepoPath string
	DiffFile string
	Mode     ReviewMode
	Executor string // local, container, e2b
	Output   string // json, md, text
	DBPath   string
}

// ReviewResult 审查结果
type ReviewResult struct {
	TaskID   string
	Report   *report.ReviewReport
	Duration time.Duration
	Error    error
}

// Orchestrator 审查编排器
type Orchestrator struct {
	db             *sql.DB
	ruleEngine     *rules.RuleEngine
	permPolicy     *security.PermissionPolicy
	secretDetector *security.SecretDetector
}

// New 创建编排器
func New(dbPath string) (*Orchestrator, error) {
	db, err := store.InitDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	return &Orchestrator{
		db:             db,
		ruleEngine:     rules.NewRuleEngine(),
		permPolicy:     security.NewPermissionPolicy(),
		secretDetector: security.NewSecretDetector(),
	}, nil
}

// Close 关闭编排器
func (o *Orchestrator) Close() {
	if o.db != nil {
		o.db.Close()
	}
}

// Run 执行审查流程
func (o *Orchestrator) Run(ctx context.Context, config *ReviewConfig) *ReviewResult {
	startTime := time.Now()
	result := &ReviewResult{
		TaskID: config.TaskID,
	}

	// 1. 创建任务
	task := &store.ReviewTask{
		TaskID:    config.TaskID,
		RepoPath:  config.RepoPath,
		DiffFile:  config.DiffFile,
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if err := store.SaveTask(o.db, task); err != nil {
		result.Error = fmt.Errorf("save task: %w", err)
		return result
	}

	// 2. 解析输入
	parser := input.NewDiffParser(config.RepoPath)
	var diffResult *input.DiffParseResult
	var err error

	if config.DiffFile != "" {
		diffResult, err = parser.ParseFile(config.DiffFile)
	} else {
		diffResult, err = parser.ParseGitDiff(ctx)
	}

	if err != nil {
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", err.Error())
		result.Error = fmt.Errorf("parse diff: %w", err)
		return result
	}

	// 保存 diff 摘要
	for _, file := range diffResult.Files {
		if err := store.SaveDiffSummary(o.db, config.TaskID, file.Path, file.Status, file.Additions, file.Deletions); err != nil {
			log.Printf("Warning: failed to save diff summary for %s: %v", file.Path, err)
		}
	}

	// 3. 应用规则
	log.Printf("Applying rules...")
	findings := o.ruleEngine.CheckDiffResult(config.TaskID, diffResult)

	// 4. AI 增强分析
	if config.Mode == ModeFakeModel {
		log.Printf("Running fake model analysis...")
		fakeModel := model.NewFakeModel("fake-gpt")
		diffContent := readDiffContent(config.DiffFile)
		aiResponse, err := fakeModel.GenerateResponse(ctx, diffContent)
		if err != nil {
			log.Printf("Warning: fake model analysis failed: %v", err)
		} else {
			aiFindings := parseAIFindings(aiResponse, config.TaskID)
			findings = append(findings, aiFindings...)
		}
	} else if config.Mode == ModeLLM && os.Getenv("OPENAI_API_KEY") != "" {
		log.Printf("Running LLM analysis...")
		// LLM 分析在 main.go 中实现
	}

	// 5. 去重降噪
	uniqueFindings, warnings := rules.DeduplicateFindings(findings)

	// 6. 权限检查
	permDecision := store.PermissionDecision{
		TaskID:   config.TaskID,
		Command:  "review",
		Decision: "allow",
		Reason:   "code review operation",
	}
	store.SavePermissionDecision(o.db, &permDecision)

	// 7. 沙箱执行
	var sandboxRuns []store.SandboxRun
	if config.Mode != ModeDryRun {
		log.Printf("Running sandbox checks (executor: %s)...", config.Executor)

		var sandboxExec *sandbox.Executor
		switch config.Executor {
		case "container":
			sandboxExec = sandbox.NewExecutorWithType(config.RepoPath, sandbox.ExecutorContainer)
		case "e2b":
			sandboxExec = sandbox.NewExecutorWithType(config.RepoPath, sandbox.ExecutorE2B)
		default:
			sandboxExec = sandbox.NewExecutor(config.RepoPath)
		}

		runs, err := sandboxExec.RunAllChecks(ctx, config.TaskID)
		if err != nil {
			log.Printf("Warning: sandbox checks failed: %v", err)
		}
		sandboxRuns = runs

		for _, run := range sandboxRuns {
			store.SaveSandboxRun(o.db, &run)
		}
	}

	// 8. 敏感信息脱敏
	for i := range uniqueFindings {
		if o.secretDetector.Detect(uniqueFindings[i].Evidence) {
			uniqueFindings[i].Evidence = "<redacted>"
		}
		if o.secretDetector.Detect(uniqueFindings[i].Description) {
			uniqueFindings[i].Description = o.secretDetector.RedactText(uniqueFindings[i].Description)
		}
	}

	// 9. 保存 findings
	if err := store.SaveFindings(o.db, uniqueFindings); err != nil {
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", err.Error())
		result.Error = fmt.Errorf("save findings: %w", err)
		return result
	}

	// 10. 计算监控摘要
	monitoring := store.CalculateMonitoring(config.TaskID, uniqueFindings, sandboxRuns, startTime)
	store.SaveMonitoringSummary(o.db, monitoring)

	// 11. 生成报告
	reviewReport := report.Generate(config.TaskID, uniqueFindings, warnings, sandboxRuns, monitoring)

	// 12. 更新任务状态
	store.UpdateTaskStatus(o.db, config.TaskID, "completed", "")

	// 13. 保存报告
	reportJSON, _ := json.MarshalIndent(reviewReport, "", "  ")
	reportMD := report.GenerateMarkdownString(reviewReport)
	store.SaveReviewReport(o.db, &store.ReviewReport{
		TaskID:     config.TaskID,
		ReportJSON: string(reportJSON),
		ReportMD:   reportMD,
	})

	// 14. 保存 artifact
	store.SaveArtifact(o.db, &store.Artifact{
		TaskID:       config.TaskID,
		ArtifactType: "report_json",
		FilePath:     "review_report.json",
		Content:      string(reportJSON),
	})

	result.Report = reviewReport
	result.Duration = time.Since(startTime)

	return result
}

// readDiffContent 读取 diff 文件内容
func readDiffContent(diffFile string) string {
	content, err := os.ReadFile(diffFile)
	if err != nil {
		return ""
	}
	return string(content)
}

// parseAIFindings 解析 AI 返回的 findings
func parseAIFindings(response string, taskID string) []store.Finding {
	findings := make([]store.Finding, 0)

	var result struct {
		Findings []store.Finding `json:"findings"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
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
