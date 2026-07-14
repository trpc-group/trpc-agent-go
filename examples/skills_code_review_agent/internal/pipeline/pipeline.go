//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package pipeline orchestrates the deterministic review flow.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/llmreview"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/storage"
)

// Options configures a review run.
type Options struct {
	DiffFile    string
	RepoPath    string
	Fixture     string
	DryRun      bool
	DBPath      string
	OutputDir   string
	SkillsRoot  string
	Runtime     sandbox.Runtime
	SkipSandbox bool
	Model       string
}

// Result contains paths and identifiers produced by a run.
type Result struct {
	TaskID   string
	JSONPath string
	Markdown string
}

// Run executes the deterministic review pipeline.
func Run(ctx context.Context, opts Options) (*Result, error) {
	start := time.Now()
	parsed, err := loadInput(opts)
	if err != nil {
		return nil, err
	}

	taskID := uuid.NewString()
	runtime := opts.Runtime
	if runtime == "" {
		runtime = sandbox.RuntimeLocal
	}
	if opts.SkipSandbox {
		runtime = sandbox.RuntimeSkip
	}

	raw := rules.Analyze(parsed)
	if !opts.DryRun {
		llmFindings, err := llmreview.Run(ctx, llmreview.Options{
			TaskID:       taskID,
			DiffRaw:      parsed.Raw,
			InputSummary: parsed.Summary(),
			SkillsRoot:   opts.SkillsRoot,
			Runtime:      runtime,
			Model:        opts.Model,
			RuleFindings: raw,
		})
		if err != nil {
			return nil, fmt.Errorf("llm review: %w", err)
		}
		raw = findings.Merge(raw, llmFindings)
	}
	merged := findings.Dedup(raw)
	confirmed, warnings := findings.Partition(merged)

	var sandboxResult *sandbox.Result
	if runtime != sandbox.RuntimeSkip {
		sandboxResult, _ = sandbox.Run(ctx, sandbox.Options{
			TaskID:     taskID,
			DiffRaw:    parsed.Raw,
			RepoPath:   opts.RepoPath,
			SkillsRoot: opts.SkillsRoot,
			Runtime:    runtime,
		})
	}
	if sandboxResult == nil {
		sandboxResult = &sandbox.Result{Exceptions: map[string]int{}}
	}

	confirmed = redact.RedactFindings(confirmed)
	warnings = redact.RedactFindings(warnings)

	durationMs := int(time.Since(start).Milliseconds())
	reviewResult := &findings.ReviewResult{
		TaskID:              taskID,
		Status:              "completed",
		InputSummary:        redact.RedactString(parsed.Summary()),
		RepoPath:            opts.RepoPath,
		Findings:            confirmed,
		Warnings:            warnings,
		PermissionDecisions: toPermissionSummaries(sandboxResult.Permissions),
		SandboxRuns:         toSandboxSummaries(sandboxResult.Runs),
		Metrics: findings.BuildMetrics(confirmed, warnings, findings.MetricsInput{
			TotalDurationMs:     durationMs,
			SandboxDurationMs:   sandboxResult.DurationMs,
			ToolCallCount:       sandboxResult.ToolCalls,
			PermissionDenyCount: sandboxResult.DenyCount,
			ExceptionCounts:     sandboxResult.Exceptions,
		}),
		DryRun:         opts.DryRun,
		SandboxRuntime: string(runtime),
	}

	jsonPath := filepath.Join(opts.OutputDir, "review_report.json")
	mdPath := filepath.Join(opts.OutputDir, "review_report.md")
	if err := report.WriteJSON(jsonPath, reviewResult); err != nil {
		return nil, err
	}
	if err := report.WriteMarkdown(mdPath, reviewResult); err != nil {
		return nil, err
	}

	jsonBytes, err := json.MarshalIndent(reviewResult, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal review json: %w", err)
	}
	mdBytes, err := readFile(mdPath)
	if err != nil {
		return nil, err
	}

	store, err := storage.NewSQLiteStore("file:" + opts.DBPath + "?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	record := &storage.ReviewRecord{
		TaskID:       taskID,
		Status:       reviewResult.Status,
		InputSummary: reviewResult.InputSummary,
		RepoPath:     opts.RepoPath,
		CreatedAt:    now,
		FinishedAt:   now,
		DurationMs:   durationMs,
		Findings:     confirmed,
		Warnings:     warnings,
		Metrics:      reviewResult.Metrics,
		Artifacts: []storage.ArtifactRecord{
			{ID: uuid.NewString(), TaskID: taskID, Name: "review_report.json", Content: string(jsonBytes)},
			{ID: uuid.NewString(), TaskID: taskID, Name: "review_report.md", Content: string(mdBytes)},
		},
		PermissionDecisions: toStoragePermissions(sandboxResult.Permissions),
		SandboxRuns:         toStorageSandboxRuns(sandboxResult.Runs),
	}
	if err := store.SaveReview(ctx, record); err != nil {
		return nil, err
	}

	return &Result{
		TaskID:   taskID,
		JSONPath: jsonPath,
		Markdown: mdPath,
	}, nil
}

func toPermissionSummaries(items []sandbox.PermissionRecord) []findings.PermissionDecision {
	out := make([]findings.PermissionDecision, 0, len(items))
	for _, p := range items {
		out = append(out, findings.PermissionDecision{
			ToolName: p.ToolName,
			Command:  p.Command,
			Action:   p.Action,
			Reason:   p.Reason,
		})
	}
	return out
}

func toSandboxSummaries(items []sandbox.RunRecord) []findings.SandboxRunSummary {
	out := make([]findings.SandboxRunSummary, 0, len(items))
	for _, r := range items {
		out = append(out, findings.SandboxRunSummary{
			Command:    r.Command,
			Runtime:    r.Runtime,
			Status:     r.Status,
			ExitCode:   r.ExitCode,
			DurationMs: r.DurationMs,
			ErrorType:  r.ErrorType,
			Stdout:     r.Stdout,
			Stderr:     r.Stderr,
		})
	}
	return out
}

func toStoragePermissions(items []sandbox.PermissionRecord) []storage.PermissionRecord {
	out := make([]storage.PermissionRecord, 0, len(items))
	for _, p := range items {
		out = append(out, storage.PermissionRecord{
			ID: p.ID, TaskID: p.TaskID, ToolName: p.ToolName,
			Command: p.Command, Action: p.Action, Reason: p.Reason,
		})
	}
	return out
}

func toStorageSandboxRuns(items []sandbox.RunRecord) []storage.SandboxRunRecord {
	out := make([]storage.SandboxRunRecord, 0, len(items))
	for _, r := range items {
		out = append(out, storage.SandboxRunRecord{
			ID: r.ID, TaskID: r.TaskID, Command: r.Command, Runtime: r.Runtime,
			Status: r.Status, ExitCode: r.ExitCode, DurationMs: r.DurationMs,
			Stdout: r.Stdout, Stderr: r.Stderr, ErrorType: r.ErrorType,
		})
	}
	return out
}

func loadInput(opts Options) (*diff.Diff, error) {
	switch {
	case opts.Fixture != "":
		path := filepath.Join("fixtures", opts.Fixture+".diff")
		return diff.LoadFromFile(path)
	case opts.DiffFile != "":
		return diff.LoadFromFile(opts.DiffFile)
	case opts.RepoPath != "":
		return diff.LoadFromRepo(opts.RepoPath)
	default:
		return nil, fmt.Errorf("one of --fixture, --diff-file, or --repo-path is required")
	}
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
