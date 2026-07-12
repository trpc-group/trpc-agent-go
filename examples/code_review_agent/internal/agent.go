//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReviewInput holds the input for a code review.
type ReviewInput struct {
	DiffFile  string   // path to the diff file
	RepoPath  string   // optional: git repo path for sandbox execution
	DryRun    bool     // if true, skip sandbox execution
	TaskID    string   // optional: pre-set task ID
	FilePaths []string // optional: repository-relative path filter
}

// ReviewResult holds the result of a code review.
type ReviewResult struct {
	TaskID            string
	Task              *ReviewTask
	Findings          []Finding
	Warnings          []Warning
	SandboxRuns       []SandboxRun
	PermissionRecords []PermissionRecord
	Monitoring        *MonitoringSummary
	ReportJSON        string
	ReportMD          string
}

// ReviewAgent orchestrates the full code review pipeline.
type ReviewAgent struct {
	storage Storage
	rules   *RuleEngine
	policy  *PermissionPolicy
	sandbox SandboxExecutor
}

// NewReviewAgentWithSandbox creates an agent with an explicitly selected
// execution backend. Production callers should pass a container-backed
// executor; NewReviewAgentWithConfig is the local development fallback.
func NewReviewAgentWithSandbox(storage Storage, sandbox SandboxExecutor) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: sandbox,
	}
}

// NewReviewAgent creates a ReviewAgent with the given storage and
// default rules, policy, and sandbox.
func NewReviewAgent(storage Storage) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: NewDefaultSandbox(),
	}
}

// NewReviewAgentWithConfig creates a ReviewAgent with custom sandbox
// config.
func NewReviewAgentWithConfig(storage Storage, sandboxCfg SandboxConfig) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: NewSandbox(sandboxCfg),
	}
}

// Review runs the full code review pipeline on the given input.
func (a *ReviewAgent) Review(ctx context.Context, input ReviewInput) (*ReviewResult, error) {
	taskID := input.TaskID
	if taskID == "" {
		taskID = "review-" + uuid.NewString()[:8]
	}

	monitor := NewMonitor(taskID)

	// 1. Create review task.
	task := &ReviewTask{
		ID:        taskID,
		InputType: "diff",
		InputPath: input.DiffFile,
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if input.DryRun {
		task.InputType = "diff-dry-run"
	}
	if err := a.storage.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save task: %w", err)
	}

	// 2. Parse diff.
	diffContent, inputType, err := LoadReviewInput(ctx, input)
	if err != nil {
		task.Status = "failed"
		_ = a.storage.UpdateTaskStatus(ctx, taskID, "failed", time.Now(), monitor.Finalize().TotalDurationMs)
		return nil, err
	}
	task.InputType = inputType
	if input.DryRun {
		task.InputType += "-dry-run"
	}
	if input.DiffFile == "" {
		task.InputPath = input.RepoPath
	}

	files, err := ParseDiff(strings.NewReader(string(diffContent)))
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	diffSummary := DiffSummary(files)
	task.DiffSummary = diffSummary
	if err := a.storage.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save diff summary: %w", err)
	}

	// 3. Run rules.
	monitor.RecordToolCall()
	rawFindings := a.rules.Run(files)

	// 4. Redact sensitive info (already done in RuleEngine.Run, but
	// double-check here for safety).
	for i := range rawFindings {
		rawFindings[i].Evidence = RedactSensitiveInfo(rawFindings[i].Evidence)
		if rawFindings[i].ID == "" {
			rawFindings[i].ID = uuid.NewString()[:8]
		}
	}

	// 5. Dedup.
	deduped := DedupFindings(rawFindings)

	// 6. Split into findings + warnings.
	findings, warnings := SplitFindings(deduped)

	// Record findings in monitor.
	for _, f := range findings {
		monitor.RecordFinding(f)
	}
	for range warnings {
		monitor.RecordWarning()
	}

	// 7. Permission decisions + sandbox execution.
	var permissionRecords []PermissionRecord
	var sandboxRuns []SandboxRun

	if !input.DryRun && input.RepoPath != "" {
		sandboxCommands := a.determineSandboxCommands(files, input.RepoPath)

		for _, cmd := range sandboxCommands {
			decision, reason := a.policy.Decide(cmd)
			rec := &PermissionRecord{
				ID:        uuid.NewString()[:8],
				TaskID:    taskID,
				Command:   cmd,
				Decision:  decision,
				Reason:    reason,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}
			permissionRecords = append(permissionRecords, *rec)
			if err := a.storage.SavePermissionDecision(ctx, rec); err != nil {
				return nil, fmt.Errorf("save permission decision: %w", err)
			}

			monitor.RecordToolCall()
			if IsBlocked(decision) {
				monitor.RecordPermissionBlock()
				continue
			}

			// Execute in sandbox.
			monitor.StartSandbox()
			run := a.sandbox.Execute(ctx, taskID, cmd, decision, reason)
			sandboxRuns = append(sandboxRuns, *run)
			monitor.EndSandbox()

			if err := a.storage.SaveSandboxRun(ctx, run); err != nil {
				return nil, fmt.Errorf("save sandbox run: %w", err)
			}

			if run.Status == SandboxStatusTimeout {
				monitor.RecordError("sandbox_timeout")
			} else if run.Status == SandboxStatusFailed {
				monitor.RecordError("sandbox_failed")
			} else if run.Status == SandboxStatusError {
				monitor.RecordError("sandbox_error")
			}
		}
	}

	// 8. Save findings to storage.
	for i := range findings {
		if findings[i].ID == "" {
			findings[i].ID = uuid.NewString()[:8]
		}
		if err := a.storage.SaveFinding(ctx, taskID, &findings[i]); err != nil {
			return nil, fmt.Errorf("save finding: %w", err)
		}
	}

	// 9. Generate reports.
	monitoring := monitor.Finalize()
	if err := a.storage.SaveMonitoring(ctx, monitoring); err != nil {
		return nil, fmt.Errorf("save monitoring summary: %w", err)
	}

	reportData := NewReportData(
		taskID, diffSummary, findings, warnings,
		permissionRecords, sandboxRuns, monitoring,
	)
	jsonReport, err := GenerateJSONReport(reportData)
	if err != nil {
		return nil, fmt.Errorf("generate json report: %w", err)
	}
	mdReport := GenerateMarkdownReport(reportData)

	report := &ReviewReport{
		ID:         uuid.NewString()[:8],
		TaskID:     taskID,
		ReportJSON: jsonReport,
		ReportMD:   mdReport,
		CreatedAt:  time.Now(),
	}
	if err := a.storage.SaveReport(ctx, report); err != nil {
		return nil, fmt.Errorf("save report: %w", err)
	}
	for _, artifact := range []*Artifact{
		{ID: uuid.NewString()[:8], TaskID: taskID, Name: "review_report.json", MIMEType: "application/json", Size: int64(len(jsonReport)), CreatedAt: time.Now()},
		{ID: uuid.NewString()[:8], TaskID: taskID, Name: "review_report.md", MIMEType: "text/markdown", Size: int64(len(mdReport)), CreatedAt: time.Now()},
	} {
		if err := a.storage.SaveArtifact(ctx, artifact); err != nil {
			return nil, fmt.Errorf("save artifact metadata: %w", err)
		}
	}

	// 10. Update task status.
	completedAt := time.Now()
	if err := a.storage.UpdateTaskStatus(ctx, taskID, "completed", completedAt, monitoring.TotalDurationMs); err != nil {
		return nil, fmt.Errorf("complete review task: %w", err)
	}

	task.Status = "completed"
	task.CompletedAt = &completedAt
	task.TotalDurationMs = monitoring.TotalDurationMs

	return &ReviewResult{
		TaskID:            taskID,
		Task:              task,
		Findings:          findings,
		Warnings:          warnings,
		SandboxRuns:       sandboxRuns,
		PermissionRecords: permissionRecords,
		Monitoring:        monitoring,
		ReportJSON:        jsonReport,
		ReportMD:          mdReport,
	}, nil
}

// determineSandboxCommands figures out what commands to run in the
// sandbox based on the changed files.
func (a *ReviewAgent) determineSandboxCommands(files []DiffFile, repoPath string) []string {
	var cmds []string
	hasGoFiles := false
	for _, f := range files {
		if !f.IsDeleted && strings.HasSuffix(f.Path, ".go") {
			hasGoFiles = true
			break
		}
	}
	if hasGoFiles {
		cmds = append(cmds, "go vet ./...")
		cmds = append(cmds, "go test ./... -count=1 -timeout=30s")
	}
	return cmds
}
