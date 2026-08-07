//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package app wires together the code review pipeline.
package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/analysis"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

// Config holds the configuration for a review run.
type Config struct {
	DiffFile        string
	RepoPath        string
	DryRun          bool
	SandboxType     sandbox.RuntimeType
	DBPath          string
	OutputJSON      string
	OutputMD        string
	AllowedCommands []string
	DeniedCommands  []string
}

// Run executes the full review pipeline and returns the report path.
func Run(ctx context.Context, cfg Config) error {
	taskID := uuid.New().String()
	startTime := time.Now()

	store, err := store.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer store.Close()

	// Determine sandbox type.
	st := cfg.SandboxType
	if st == "" {
		st = sandbox.RuntimeContainer
	}
	if cfg.DryRun {
		st = sandbox.RuntimeFake
	}

	task := &reviewmodel.ReviewTask{
		ID:          taskID,
		DiffFile:    cfg.DiffFile,
		RepoPath:    cfg.RepoPath,
		Status:      reviewmodel.StatusRunning,
		DryRun:      cfg.DryRun,
		SandboxType: string(st),
		CreatedAt:   time.Now(),
	}
	if err := store.CreateTask(ctx, task); err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	// Load input.
	in, err := loadInput(cfg)
	if err != nil {
		task.Status = reviewmodel.StatusFailed
		task.ErrorMessage = err.Error()
		store.Finalize(ctx, taskID, task)
		return fmt.Errorf("load input: %w", err)
	}

	var diffText string
	var repoPath string
	if in.DiffText != "" {
		diffText = in.DiffText
	} else if in.RepoPath != "" {
		diffText, err = generateRepoDiff(in.RepoPath)
		if err != nil {
			task.Status = reviewmodel.StatusFailed
			task.ErrorMessage = err.Error()
			store.Finalize(ctx, taskID, task)
			return fmt.Errorf("generate repo diff: %w", err)
		}
		repoPath = in.RepoPath
	}

	// Parse diff.
	pd, err := diffparse.Parse(diffText)
	if err != nil {
		task.Status = reviewmodel.StatusFailed
		task.ErrorMessage = err.Error()
		store.Finalize(ctx, taskID, task)
		return fmt.Errorf("parse diff: %w", err)
	}

	totalFiles := len(pd.Files)
	totalHunks := 0
	for _, cf := range pd.Files {
		totalHunks += len(cf.Hunks)
	}
	task.DiffSummary = fmt.Sprintf(`{"files":%d,"hunks":%d}`, totalFiles, totalHunks)

	// Governance and sandbox execution.
	permissionDecisions, sandboxRuns, sandboxDur := runGovernedSandbox(
		ctx, cfg, st, taskID, repoPath, store,
	)

	// Run analysis.
	analyzer := analysis.NewAnalyzer()
	findings := analyzer.Analyze(pd)
	findings = analysis.Normalize(findings)

	// Save findings and finalize.
	for i := range findings {
		_ = store.SaveFinding(ctx, taskID, &findings[i])
	}

	task.TotalDurationMs = time.Since(startTime).Milliseconds()
	task.SandboxDurationMs = sandboxDur
	task.ToolCallCount = len(sandboxRuns)

	task.Status = reviewmodel.StatusCompleted
	if len(findings) > 0 {
		task.Status = reviewmodel.StatusCompletedWithWarnings
	}

	reviewReport := report.NewReport(
		task, findings, sandboxRuns, permissionDecisions, totalFiles, totalHunks,
	)

	if err := writeReports(cfg, reviewReport); err != nil {
		return fmt.Errorf("write reports: %w", err)
	}

	if err := store.Finalize(ctx, taskID, task); err != nil {
		return fmt.Errorf("finalize task: %w", err)
	}

	fmt.Printf("Review complete. Task: %s, Findings: %d\n", taskID, len(findings))
	fmt.Printf("JSON report: %s\n", cfg.OutputJSON)
	fmt.Printf("Markdown report: %s\n", cfg.OutputMD)

	return nil
}

func runGovernedSandbox(
	ctx context.Context, cfg Config, st sandbox.RuntimeType,
	taskID, repoPath string, s store.ReviewStore,
) ([]reviewmodel.PermissionDecision, []reviewmodel.SandboxRun, int64) {
	policy := governance.NewPolicy(cfg.AllowedCommands, cfg.DeniedCommands, cfg.DryRun)
	sandboxDecision := policy.Check("go")
	decisions := []reviewmodel.PermissionDecision{
		{ToolName: "go_vet", Action: sandboxDecision.Action, Reason: sandboxDecision.Reason},
		{ToolName: "go_test", Action: sandboxDecision.Action, Reason: sandboxDecision.Reason},
	}
	for i := range decisions {
		_ = s.SavePermissionDecision(ctx, taskID, &decisions[i])
	}

	exec := sandbox.NewExecutor(st)
	var runs []reviewmodel.SandboxRun
	sandboxStart := time.Now()

	if repoPath != "" && sandboxDecision.Allowed {
		run := exec.RunGoVet(ctx, repoPath)
		run.ID = uuid.New().String()
		_ = s.SaveSandboxRun(ctx, taskID, run)
		runs = append(runs, *run)

		run2 := exec.RunGoTest(ctx, repoPath)
		run2.ID = uuid.New().String()
		_ = s.SaveSandboxRun(ctx, taskID, run2)
		runs = append(runs, *run2)
	}

	return decisions, runs, time.Since(sandboxStart).Milliseconds()
}

func writeReports(cfg Config, r *reviewmodel.ReviewReport) error {
	jsonPath := cfg.OutputJSON
	if jsonPath == "" {
		jsonPath = "review_report.json"
	}
	if err := report.GenerateJSON(r, jsonPath); err != nil {
		return fmt.Errorf("generate json report: %w", err)
	}
	mdPath := cfg.OutputMD
	if mdPath == "" {
		mdPath = "review_report.md"
	}
	if err := report.GenerateMarkdown(r, mdPath); err != nil {
		return fmt.Errorf("generate markdown report: %w", err)
	}
	return nil
}

func loadInput(cfg Config) (*input.Input, error) {
	if cfg.DiffFile != "" {
		return input.LoadFromDiffFile(cfg.DiffFile)
	}
	if cfg.RepoPath != "" {
		return input.LoadFromRepoPath(cfg.RepoPath)
	}
	return nil, fmt.Errorf("no input source specified: use --diff-file or --repo-path")
}

func generateRepoDiff(repoPath string) (string, error) {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return "", fmt.Errorf("read repo dir: %w", err)
	}
	var diffBuilder string
	for _, entry := range entries {
		if entry.IsDir() || !isGoFile(entry.Name()) {
			continue
		}
		fullPath := repoPath + "/" + entry.Name()
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		content := string(data)
		diffBuilder += fmt.Sprintf("diff --git a/%s b/%s\n", entry.Name(), entry.Name())
		diffBuilder += fmt.Sprintf("--- a/%s\n+++ b/%s\n", entry.Name(), entry.Name())
		lines := splitLines(content)
		diffBuilder += fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines))
		for _, l := range lines {
			diffBuilder += "+" + l + "\n"
		}
	}
	return diffBuilder, nil
}

func splitLines(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func isGoFile(name string) bool {
	return len(name) > 3 && name[len(name)-3:] == ".go"
}
