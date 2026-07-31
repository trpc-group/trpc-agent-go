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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
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
	TaskID      string
	RepoPath    string
	DiffFile    string
	Mode        ReviewMode
	Executor    string // local, container, e2b
	Output      string // json, md, text
	DBPath      string
	LLMAnalyzer LLMAnalyzer
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
			result.Error = fmt.Errorf("save diff summary for %s: %w", file.Path, err)
			store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
			result.Duration = time.Since(startTime)
			return result
		}
	}

	// 3. 应用规则
	log.Printf("Applying rules...")
	findings := o.ruleEngine.CheckDiffResult(config.TaskID, diffResult)

	// 4. AI 增强分析
	if config.Mode == ModeFakeModel {
		log.Printf("Running fake model analysis...")
		aiFindings, err := analyzeWithFakeModel(ctx, config.TaskID, diffResult)
		if err != nil {
			result.Error = fmt.Errorf("fake model analysis: %w", err)
			store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
			result.Duration = time.Since(startTime)
			return result
		}
		findings = append(findings, aiFindings...)
	} else if config.Mode == ModeLLM {
		analyzer := config.LLMAnalyzer
		if analyzer == nil {
			analyzer = NewOpenAIAnalyzer("")
		}
		aiFindings, err := analyzer(ctx, config.TaskID, diffResult)
		if err != nil {
			result.Error = fmt.Errorf("LLM analysis: %w", err)
			store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
			result.Duration = time.Since(startTime)
			return result
		}
		findings = append(findings, aiFindings...)
	}

	// 5. 去重降噪
	uniqueFindings, warnings := rules.DeduplicateFindings(findings)

	// 6. 权限检查
	decision := o.permPolicy.Evaluate("review")
	permDecision := store.PermissionDecision{
		TaskID:   config.TaskID,
		Command:  "review",
		Decision: decision.Decision,
		Reason:   decision.Reason,
	}
	if err := store.SavePermissionDecision(o.db, &permDecision); err != nil {
		result.Error = fmt.Errorf("save permission decision: %w", err)
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
		result.Duration = time.Since(startTime)
		return result
	}

	// 7. 沙箱执行
	var sandboxRuns []store.SandboxRun
	if config.Mode != ModeDryRun {
		log.Printf("Running sandbox checks (executor: %s)...", config.Executor)

		var sandboxExec *sandbox.Executor
		switch config.Executor {
		case "local":
			sandboxExec = sandbox.NewExecutor(config.RepoPath)
		case "container":
			sandboxExec = sandbox.NewExecutorWithType(config.RepoPath, sandbox.ExecutorContainer)
		case "e2b":
			sandboxExec = sandbox.NewExecutorWithType(config.RepoPath, sandbox.ExecutorE2B)
		default:
			result.Error = fmt.Errorf("unsupported executor %q", config.Executor)
			store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
			result.Duration = time.Since(startTime)
			return result
		}

		runs, err := sandboxExec.RunAllChecks(ctx, config.TaskID)
		if err != nil {
			result.Error = fmt.Errorf("sandbox checks: %w", err)
			store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
			result.Duration = time.Since(startTime)
			return result
		}
		sandboxRuns = runs
		for _, run := range sandboxRuns {
			if run.ExitCode != 0 {
				result.Error = fmt.Errorf("sandbox check %s failed with exit code %d", run.ScriptName, run.ExitCode)
				store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
				result.Duration = time.Since(startTime)
				return result
			}
		}

		for _, run := range sandboxRuns {
			if err := store.SaveSandboxRun(o.db, &run); err != nil {
				result.Error = fmt.Errorf("save sandbox run: %w", err)
				store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
				result.Duration = time.Since(startTime)
				return result
			}
		}
	}

	// 8. 敏感信息脱敏
	o.secretDetector.RedactFindings(uniqueFindings)
	o.secretDetector.RedactFindings(warnings)

	// 9. 保存 findings
	if err := store.SaveFindings(o.db, uniqueFindings); err != nil {
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", err.Error())
		result.Error = fmt.Errorf("save findings: %w", err)
		return result
	}

	// 10. 计算监控摘要
	monitoring := store.CalculateMonitoring(config.TaskID, uniqueFindings, sandboxRuns, startTime)
	if err := store.SaveMonitoringSummary(o.db, monitoring); err != nil {
		result.Error = fmt.Errorf("save monitoring summary: %w", err)
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
		result.Duration = time.Since(startTime)
		return result
	}

	// 11. 生成报告
	reviewReport := report.Generate(config.TaskID, uniqueFindings, warnings, sandboxRuns, monitoring)

	// 12. 保存报告
	reportJSON, err := json.MarshalIndent(reviewReport, "", "  ")
	if err != nil {
		result.Error = fmt.Errorf("marshal report: %w", err)
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
		result.Duration = time.Since(startTime)
		return result
	}
	reportMD := report.GenerateMarkdownString(reviewReport)
	if err := store.SaveReviewReport(o.db, &store.ReviewReport{
		TaskID:     config.TaskID,
		ReportJSON: string(reportJSON),
		ReportMD:   reportMD,
	}); err != nil {
		result.Error = fmt.Errorf("save report: %w", err)
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
		result.Duration = time.Since(startTime)
		return result
	}

	// 13. 保存 artifact
	if err := store.SaveArtifact(o.db, &store.Artifact{
		TaskID:       config.TaskID,
		ArtifactType: "report_json",
		FilePath:     "review_report.json",
		Content:      string(reportJSON),
	}); err != nil {
		result.Error = fmt.Errorf("save report artifact: %w", err)
		store.UpdateTaskStatus(o.db, config.TaskID, "failed", result.Error.Error())
		result.Duration = time.Since(startTime)
		return result
	}
	if err := store.UpdateTaskStatus(o.db, config.TaskID, "completed", ""); err != nil {
		result.Error = fmt.Errorf("complete task: %w", err)
		result.Duration = time.Since(startTime)
		return result
	}

	result.Report = reviewReport
	result.Duration = time.Since(startTime)

	return result
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
