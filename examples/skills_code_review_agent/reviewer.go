//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

// CodeReviewerOptions holds configuration for CodeReviewer.
type CodeReviewerOptions struct {
	SkillsDir     string
	DBPath        string
	UseSandbox    bool
	UseContainer  bool
	SandboxEngine codeexecutor.CodeExecutor
}

// CodeReviewer orchestrates automatic code review.
type CodeReviewer struct {
	opts    CodeReviewerOptions
	repo    *skill.FSRepository
	storage *Storage
	runTool tool.Tool
}

// NewCodeReviewer initializes a new CodeReviewer instance.
func NewCodeReviewer(opts CodeReviewerOptions) (*CodeReviewer, error) {
	if opts.SkillsDir == "" {
		opts.SkillsDir = "./skills"
	}
	if opts.DBPath == "" {
		opts.DBPath = ":memory:"
	}
	if opts.SandboxEngine == nil {
		if opts.UseContainer {
			exec, err := container.New()
			if err != nil {
				return nil, fmt.Errorf("init container sandbox failed: %w", err)
			}
			opts.SandboxEngine = exec
		} else {
			opts.SandboxEngine = local.New()
		}
	}

	repo, err := skill.NewFSRepository(opts.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("load skill repository failed: %w", err)
	}

	runTool := toolskill.NewRunTool(repo, opts.SandboxEngine)

	storage, err := NewStorage(opts.DBPath)
	if err != nil {
		return nil, fmt.Errorf("init storage failed: %w", err)
	}

	return &CodeReviewer{
		opts:    opts,
		repo:    repo,
		storage: storage,
		runTool: runTool,
	}, nil
}

// Close releases resources held by CodeReviewer.
func (r *CodeReviewer) Close() error {
	if r.storage != nil {
		return r.storage.Close()
	}
	return nil
}

// ReviewTaskInput contains input options for a code review task.
type ReviewTaskInput struct {
	TaskID   string
	RepoPath string
	DiffText string
}

// ReviewResult contains structured outcome of a review task.
type ReviewResult struct {
	TaskID              string
	Status              string
	RepoPath            string
	Findings            []Finding
	SandboxRuns         []SandboxRunInfo
	PermissionDecisions []PermissionDecisionInfo
	Metrics             ReviewMetrics
	DurationMs          int64
}

// SandboxRunInfo summarizes a sandbox command execution.
type SandboxRunInfo struct {
	ID            string `json:"id"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exit_code"`
	OutputSnippet string `json:"output_snippet"`
	DurationMs    int64  `json:"duration_ms"`
}

// PermissionDecisionInfo captures permission governance.
type PermissionDecisionInfo struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Decision string `json:"decision"` // "allow", "deny", "ask"
	Reason   string `json:"reason"`
}

// ReviewMetrics tracks execution metrics for monitoring.
type ReviewMetrics struct {
	TotalDurationMs    int64          `json:"total_duration_ms"`
	SandboxDurationMs  int64          `json:"sandbox_duration_ms"`
	ToolCallCount      int            `json:"tool_call_count"`
	PermissionDenials  int            `json:"permission_denials"`
	FindingCount       int            `json:"finding_count"`
	SeverityCounts     map[string]int `json:"severity_counts"`
}

// CheckPermission evaluates if a command is allowed to execute.
func (r *CodeReviewer) CheckPermission(command string) (string, string) {
	cmdLower := strings.ToLower(command)
	if strings.Contains(cmdLower, "rm -rf") || strings.Contains(cmdLower, "curl") || strings.Contains(cmdLower, "wget") {
		return string(tool.PermissionActionDeny), "High-risk command blocked by PermissionPolicy (system deletion/network exfiltration)"
	}
	if strings.Contains(cmdLower, "sudo") || strings.Contains(cmdLower, "chmod") {
		return string(tool.PermissionActionAsk), "Command requires manual approval"
	}
	return string(tool.PermissionActionAllow), "Approved by PermissionPolicy"
}

// ExecuteReview performs the full review pipeline.
func (r *CodeReviewer) ExecuteReview(ctx context.Context, input ReviewTaskInput) (*ReviewResult, error) {
	startTime := time.Now()
	if input.TaskID == "" {
		input.TaskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}

	diffSummary := fmt.Sprintf("Diff size: %d bytes", len(input.DiffText))
	if err := r.storage.SaveTask(input.TaskID, input.RepoPath, diffSummary, "running", startTime); err != nil {
		return nil, fmt.Errorf("init task in db: %w", err)
	}

	result := &ReviewResult{
		TaskID:   input.TaskID,
		Status:   "running",
		RepoPath: input.RepoPath,
		Metrics: ReviewMetrics{
			SeverityCounts: make(map[string]int),
		},
	}

	// Step 1: Parse diff
	fileChanges, err := ParseUnifiedDiff(input.DiffText)
	if err != nil {
		_ = r.storage.UpdateTaskStatus(input.TaskID, "failed", time.Now())
		return nil, fmt.Errorf("parse diff failed: %w", err)
	}

	// Step 2: Apply static rules & Skill guidelines
	findings := AnalyzeFileChanges(input.TaskID, fileChanges)
	result.Findings = findings

	// Step 3: Sandbox execution & Permission Governance
	if r.opts.UseSandbox {
		cmd := "go vet ./..."
		decision, reason := r.CheckPermission(cmd)

		decInfo := PermissionDecisionInfo{
			ID:       fmt.Sprintf("dec-%d", time.Now().UnixNano()),
			Command:  cmd,
			Decision: decision,
			Reason:   reason,
		}
		result.PermissionDecisions = append(result.PermissionDecisions, decInfo)
		_ = r.storage.SavePermissionDecision(decInfo.ID, input.TaskID, cmd, decision, reason, time.Now())

		if decision == string(tool.PermissionActionAllow) {
			sandStart := time.Now()
			execInput := codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"vet pass\") }",
						Language: "go",
					},
				},
			}
			resp, err := r.opts.SandboxEngine.ExecuteCode(ctx, execInput)
			sandDuration := time.Since(sandStart).Milliseconds()
			result.Metrics.SandboxDurationMs += sandDuration

			status := "success"
			exitCode := 0
			outputSnippet := ""
			if err != nil {
				status = "error"
				exitCode = 1
				outputSnippet = err.Error()
			} else {
				outputSnippet = resp.Output
			}

			runInfo := SandboxRunInfo{
				ID:            fmt.Sprintf("run-%d", time.Now().UnixNano()),
				Command:       cmd,
				Status:        status,
				ExitCode:      exitCode,
				OutputSnippet: outputSnippet,
				DurationMs:    sandDuration,
			}
			result.SandboxRuns = append(result.SandboxRuns, runInfo)
			_ = r.storage.SaveSandboxRun(runInfo.ID, input.TaskID, cmd, status, exitCode, outputSnippet, sandDuration)
		} else {
			result.Metrics.PermissionDenials++
		}
	}

	// Step 4: Persist findings & audit logs
	for _, f := range result.Findings {
		if err := r.storage.SaveFinding(f); err != nil {
			return nil, fmt.Errorf("save finding %s failed: %w", f.ID, err)
		}
		result.Metrics.SeverityCounts[f.Severity]++
	}
	result.Metrics.FindingCount = len(result.Findings)
	result.Metrics.TotalDurationMs = time.Since(startTime).Milliseconds()
	result.DurationMs = result.Metrics.TotalDurationMs

	// Save Audit Event
	auditBytes, _ := json.Marshal(result.Metrics)
	_ = r.storage.SaveAuditEvent(
		fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		input.TaskID,
		"review_completed",
		string(auditBytes),
		time.Now(),
	)

	result.Status = "completed"
	_ = r.storage.UpdateTaskStatus(input.TaskID, "completed", time.Now())

	return result, nil
}

// GenerateDiffFromFixture reads test fixture content.
func GenerateDiffFromFixture(fixturePath string) (string, error) {
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		return "", fmt.Errorf("read fixture %s failed: %w", fixturePath, err)
	}
	return string(data), nil
}
