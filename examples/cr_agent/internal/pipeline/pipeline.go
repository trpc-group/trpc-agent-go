//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pipeline orchestrates the end-to-end code review flow:
// input parsing, rule evaluation, sandbox execution, deduplication,
// report generation, and database persistence.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/dedup"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// Config configures the review pipeline.
type Config struct {
	// ConfidenceThreshold below which findings are demoted to
	// warnings. Default 0.5.
	ConfidenceThreshold float64

	// SandboxEnabled controls whether sandbox tool runs (go vet, go
	// test) are executed. When false, only static rules run.
	SandboxEnabled bool

	// SandboxTimeout is the maximum duration for each sandbox command.
	SandboxTimeout time.Duration

	// OutputDir is where review_report.json and review_report.md are
	// written. If empty, the current directory is used.
	OutputDir string

	// RepoPath is used when InputType == InputTypeGit to run git diff.
	RepoPath string
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		ConfidenceThreshold: 0.5,
		SandboxEnabled:      true,
		SandboxTimeout:      60 * time.Second,
		OutputDir:           ".",
	}
}

// Pipeline coordinates the review flow.
type Pipeline struct {
	config   Config
	store    storage.Store
	registry *rules.Registry
	sandbox  *sandbox.Executor
}

// New creates a Pipeline with the given store and config.
func New(store storage.Store, config Config) *Pipeline {
	sbCfg := sandbox.Config{
		Timeout:     config.SandboxTimeout,
		OutputLimit: 64 * 1024,
	}
	return &Pipeline{
		config:   config,
		store:    store,
		registry: rules.NewRegistry(),
		sandbox:  sandbox.NewExecutor(nil, sbCfg),
	}
}

// Run executes a full review for the given input and returns the
// generated ReviewReport. The report is also persisted to the store
// and written to disk (JSON + Markdown).
func (p *Pipeline) Run(ctx context.Context, input types.ReviewInput) (
	*types.ReviewReport, error,
) {
	overallStart := time.Now()

	// 1. Create the review task.
	taskID := uuid.NewString()
	task := &types.ReviewTask{
		ID:        taskID,
		Status:    types.StatusPending,
		CreatedAt: time.Now(),
	}
	if err := p.store.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save initial task: %w", err)
	}

	// 2. Parse input into diff text.
	parseStart := time.Now()
	diffText, err := p.resolveInput(ctx, input)
	if err != nil {
		p.failTask(ctx, taskID, err)
		return nil, fmt.Errorf("resolve input: %w", err)
	}
	parseDuration := time.Since(parseStart)

	// 3. Parse the diff.
	fileChanges, err := diff.Parse(diffText)
	if err != nil {
		p.failTask(ctx, taskID, err)
		return nil, fmt.Errorf("parse diff: %w", err)
	}

	// 4. Compute diff summary.
	diffSummary := p.computeDiffSummary(diffText, fileChanges)

	// 5. Transition to running.
	task.Status = types.StatusRunning
	task.StartedAt = time.Now()
	task.Input = diffSummary
	if err := p.store.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save running task: %w", err)
	}

	// 6. Run sandbox checks (if enabled).
	var sandboxRuns []types.SandboxRun
	var sandboxDurationMs int64
	var permissionDenials int
	var toolCalls int

	if p.config.SandboxEnabled && p.config.RepoPath != "" {
		sandboxRuns, sandboxDurationMs, permissionDenials, toolCalls =
			p.runSandboxChecks(ctx, taskID, p.config.RepoPath)
	}

	// 7. Run static rules.
	reviewStart := time.Now()
	var allFindings []types.Finding
	rulesEvaluated := 0
	for _, fc := range fileChanges {
		if fc.Status == "deleted" {
			continue
		}
		for _, rule := range p.registry.Rules() {
			rulesEvaluated++
			finds := rule.Evaluate(&fc)
			allFindings = append(allFindings, finds...)
		}
	}
	reviewDuration := time.Since(reviewStart)

	// 8. Deduplicate and demote.
	dedupedFindings, warnings := dedup.Apply(
		allFindings, p.config.ConfidenceThreshold,
	)

	// 9. Save findings and sandbox runs to the store.
	for _, f := range dedupedFindings {
		f := f // capture
		f.CreatedAt = time.Now()
		if err := p.store.SaveFinding(ctx, taskID, &f); err != nil {
			return nil, fmt.Errorf("save finding: %w", err)
		}
	}
	for _, sr := range sandboxRuns {
		sr := sr
		if err := p.store.SaveSandboxRun(ctx, &sr); err != nil {
			return nil, fmt.Errorf("save sandbox run: %w", err)
		}
	}

	// 10. Build the report.
	reportStart := time.Now()
	totalDuration := time.Since(overallStart)

	rpt := &types.ReviewReport{
		TaskID:      taskID,
		GeneratedAt: time.Now(),
		Findings:    dedupedFindings,
		Warnings:    warnings,
		Metrics: types.ReviewMetrics{
			TotalDurationMs:   totalDuration.Milliseconds(),
			SandboxDurationMs: sandboxDurationMs,
			ParseDurationMs:   parseDuration.Milliseconds(),
			ReviewDurationMs:  reviewDuration.Milliseconds(),
			ReportDurationMs:  time.Since(reportStart).Milliseconds(),
			ToolCalls:         toolCalls,
			SandboxRuns:       len(sandboxRuns),
			PermissionDenials: permissionDenials,
			RulesEvaluated:    rulesEvaluated,
		},
	}
	report.FillSummary(rpt, dedupedFindings, len(fileChanges))

	// 11. Write report files.
	if err := p.writeReports(rpt); err != nil {
		return nil, fmt.Errorf("write reports: %w", err)
	}

	// 12. Finalize the task.
	now := time.Now()
	task.Status = types.StatusCompleted
	task.CompletedAt = &now
	task.TotalDurationMs = totalDuration.Milliseconds()
	task.SandboxDurationMs = sandboxDurationMs
	task.ToolCalls = toolCalls
	task.PermissionDenials = permissionDenials
	task.FindingsCount = len(dedupedFindings)
	task.WarningsCount = len(warnings)
	if err := p.store.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save completed task: %w", err)
	}

	return rpt, nil
}

// resolveInput converts the ReviewInput into raw diff text.
func (p *Pipeline) resolveInput(
	ctx context.Context, input types.ReviewInput,
) (string, error) {
	switch input.Type {
	case types.InputTypeDiff:
		if input.DiffContent == "" {
			return "", fmt.Errorf("diff content is empty")
		}
		return input.DiffContent, nil

	case types.InputTypeFiles:
		return p.buildDiffFromFiles(input.FilePaths)

	case types.InputTypeGit:
		return p.buildDiffFromGit(ctx, input.RepoPath, input.CommitRange)

	default:
		return "", fmt.Errorf("unknown input type: %s", input.Type)
	}
}

// buildDiffFromFiles reads each file and constructs a pseudo-diff
// that marks all content as added.
func (p *Pipeline) buildDiffFromFiles(paths []string) (string, error) {
	var b strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		b.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", path, path))
		b.WriteString(fmt.Sprintf("--- /dev/null\n"))
		b.WriteString(fmt.Sprintf("+++ b/%s\n", path))
		b.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", strings.Count(string(data), "\n")+1))
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			b.WriteString("+")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// buildDiffFromGit runs git diff in the repo to produce the diff
// text.
func (p *Pipeline) buildDiffFromGit(
	_ context.Context, repoPath, commitRange string,
) (string, error) {
	if repoPath == "" {
		return "", fmt.Errorf("repo path is empty")
	}
	if commitRange == "" {
		commitRange = "HEAD~1..HEAD"
	}
	cmd := exec.Command("git", "-C", repoPath, "diff", commitRange)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(output), nil
}

// computeDiffSummary builds a DiffSummary from the parsed diff.
func (p *Pipeline) computeDiffSummary(
	diffText string, changes []diff.FileChange,
) types.DiffSummary {
	s := types.DiffSummary{
		FilesChanged: len(changes),
		DiffHash:     hashString(diffText),
	}
	for _, fc := range changes {
		s.AddedLines += fc.AddedLines
		s.DeletedLines += fc.DeletedLines
	}
	preview := diffText
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	s.DiffPreview = preview
	return s
}

// runSandboxChecks executes go vet and go test in the sandbox and
// records the results.
func (p *Pipeline) runSandboxChecks(
	ctx context.Context, taskID, repoPath string,
) (runs []types.SandboxRun, durationMs int64, denials int, toolCalls int) {
	commands := []string{
		"go vet ./...",
		"go test -count=1 ./... 2>&1 || true",
	}
	for _, cmd := range commands {
		toolCalls++
		result := p.sandbox.Run(ctx, cmd, repoPath)
		if result.Denied {
			denials++
			continue
		}
		sr := types.SandboxRun{
			ID:              uuid.NewString(),
			TaskID:          taskID,
			ToolName:        "sandbox",
			Command:         cmd,
			StdoutTruncated: result.Stdout,
			StderrTruncated: result.Stderr,
			ExitCode:        result.ExitCode,
			DurationMs:      result.Duration.Milliseconds(),
			TimedOut:        result.TimedOut,
			OutputBytes:     result.OutputBytes,
			CreatedAt:       time.Now(),
		}
		runs = append(runs, sr)
		durationMs += result.Duration.Milliseconds()
	}
	return
}

// writeReports writes the JSON and Markdown report files.
func (p *Pipeline) writeReports(rpt *types.ReviewReport) error {
	dir := p.config.OutputDir
	if dir == "" {
		dir = "."
	}
	jsonPath := filepath.Join(dir, "review_report.json")
	mdPath := filepath.Join(dir, "review_report.md")
	if err := report.SaveJSON(jsonPath, rpt); err != nil {
		return fmt.Errorf("save json: %w", err)
	}
	if err := report.SaveMarkdown(mdPath, rpt); err != nil {
		return fmt.Errorf("save markdown: %w", err)
	}
	return nil
}

// failTask marks the task as failed with the given error.
func (p *Pipeline) failTask(ctx context.Context, taskID string, err error) {
	task, getErr := p.store.GetTask(ctx, taskID)
	if getErr != nil {
		return
	}
	now := time.Now()
	task.Status = types.StatusFailed
	task.CompletedAt = &now
	task.ErrorMsg = err.Error()
	_ = p.store.SaveTask(ctx, task)
}

// hashString returns a hex SHA-256 hash of the input.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}
