//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	defaultMaxDiffLines    = 5000
	defaultMaxChangedFiles = 200
)

// RunReview executes the full deterministic review pipeline.
func RunReview(ctx context.Context, opts ReviewOptions) (ReviewReport, string, string, error) {
	ctx, span := otel.Tracer("trpc-agent-go/examples/code_review_agent").Start(ctx, "code_review_agent.review")
	defer span.End()
	start := time.Now().UTC()
	if err := validateReviewInputs(opts); err != nil {
		return ReviewReport{}, "", "", err
	}
	if opts.OutDir == "" {
		opts.OutDir = "code_review_agent_out"
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(opts.OutDir, "review_agent.db")
	}
	if opts.SkillsRoot == "" {
		opts.SkillsRoot = filepath.Join("code_review_agent", "skills")
	}
	skillRepo, err := skill.NewFSRepository(opts.SkillsRoot)
	if err != nil {
		return ReviewReport{}, "", "", fmt.Errorf("load skills: %w", err)
	}
	if _, err := skillRepo.Get("code-review"); err != nil {
		return ReviewReport{}, "", "", fmt.Errorf("load code-review skill: %w", err)
	}
	inputKind, raw, err := loadInput(ctx, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load input")
		return ReviewReport{}, "", "", err
	}
	summary, err := ParseUnifiedDiff(raw)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse diff")
		return ReviewReport{}, "", "", err
	}
	span.SetAttributes(
		attribute.String("input.kind", inputKind),
		attribute.String("diff.hash", summary.Hash),
		attribute.Int("diff.files", len(summary.Files)),
		attribute.Int("diff.lines", summary.LineCount),
	)
	opts.ChangedFiles = changedRepoPaths(summary)
	if opts.RepoPath != "" {
		opts.ChangedModules, err = changedModuleDirs(opts.RepoPath, opts.ChangedFiles)
		if err != nil {
			return ReviewReport{}, "", "", err
		}
	}
	taskID := "cr-" + summary.Hash[:12] + "-" + uuid.NewString()[:8]
	task := ReviewTask{
		ID:        taskID,
		InputKind: inputKind,
		DiffHash:  summary.Hash,
		Status:    taskStatusCompleted,
		StartedAt: start,
	}
	findings, warnings, needsHuman := AnalyzeDiff(summary)
	sizeGate := diffSizeGate(summary, opts)
	filterSummary := []FilterRecord{sizeGateRecord(taskID, sizeGate)}
	if sizeGate != nil {
		needsHuman = append(needsHuman, *sizeGate)
	}
	reportInput := redactDiffSummary(summary)
	gate := NewCommandGate()
	runs := []SandboxRun{}
	if sizeGate == nil {
		runner, err := NewSandboxRunner(opts)
		if err != nil {
			span.RecordError(err)
			if sandboxConfigError(opts) {
				span.SetStatus(codes.Error, "sandbox setup")
				return ReviewReport{}, "", "", err
			}
			runs = append(runs, sandboxSetupFailure(taskID, runtimeName(opts.Runtime), err))
		} else {
			defer runner.Close()
			var runErr error
			runs, runErr = runner.Run(ctx, taskID, reviewCommands(opts), gate)
			if runErr != nil {
				span.RecordError(runErr)
				runs = append(runs, sandboxSetupFailure(taskID, runtimeName(opts.Runtime), runErr))
			}
		}
	}
	for _, run := range runs {
		if run.Status == "failed" {
			task.Status = taskStatusFailed
		}
	}
	task.CompletedAt = time.Now().UTC()
	report := ReviewReport{
		Task:              task,
		Input:             reportInput,
		Findings:          findings,
		Warnings:          warnings,
		NeedsHumanReview:  needsHuman,
		PermissionSummary: gate.Records(),
		FilterSummary:     filterSummary,
		SandboxRuns:       runs,
		Artifacts:         []ArtifactRecord{},
		Conclusion:        conclusion(findings, needsHuman, runs),
	}
	report.Metrics = buildMetrics(start, runs, report.PermissionSummary, findings, warnings, needsHuman)
	jsonPath, mdPath, artifacts, err := writeReports(report, opts.OutDir)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "write reports")
		return ReviewReport{}, "", "", err
	}
	report.Artifacts = artifacts
	store, err := OpenStore(ctx, opts.DBPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "open store")
		return ReviewReport{}, "", "", err
	}
	defer store.Close()
	if err := store.SaveReport(ctx, report, jsonPath, mdPath); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save report")
		return ReviewReport{}, "", "", err
	}
	span.SetAttributes(
		attribute.Int("review.findings", len(findings)),
		attribute.Int("review.needs_human_review", len(needsHuman)),
		attribute.Int("review.sandbox_runs", len(runs)),
	)
	return report, jsonPath, mdPath, nil
}

func sizeGateRecord(taskID string, finding *Finding) FilterRecord {
	rec := FilterRecord{
		TaskID:    taskID,
		Filter:    "input.size_gate",
		Action:    "allow",
		CreatedAt: time.Now().UTC(),
	}
	if finding != nil {
		rec.Action = "needs_human_review"
		rec.Reason = finding.Evidence
	}
	return rec
}

func sandboxSetupFailure(taskID string, runtime string, err error) SandboxRun {
	now := time.Now().UTC()
	return SandboxRun{
		TaskID:      taskID,
		Runtime:     runtime,
		Command:     "sandbox setup",
		Status:      "failed",
		ExitCode:    -1,
		Output:      RedactSecrets(err.Error()),
		ErrorType:   "sandbox_error",
		StartedAt:   now,
		CompletedAt: now,
	}
}

func runtimeName(runtime string) string {
	if runtime == "" {
		return "container"
	}
	return runtime
}

func sandboxConfigError(opts ReviewOptions) bool {
	switch {
	case opts.Runtime == "", stringsEqualFold(opts.Runtime, "container"), stringsEqualFold(opts.Runtime, "e2b"), stringsEqualFold(opts.Runtime, "fake"):
		return false
	case stringsEqualFold(opts.Runtime, "local"):
		return !opts.AllowTrustedLocal
	default:
		return true
	}
}

func redactDiffSummary(summary DiffSummary) DiffSummary {
	summary.Raw = ""
	for i := range summary.AddedLines {
		summary.AddedLines[i].Content = RedactSecrets(summary.AddedLines[i].Content)
	}
	return summary
}

func stringsEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func reviewCommands(opts ReviewOptions) []string {
	if opts.DryRun && opts.RepoPath == "" {
		return []string{skillScriptCommand}
	}
	if opts.RepoPath == "" && stringsEqualFold(opts.Runtime, "fake") {
		return []string{"go test ./...", "go vet ./...", skillScriptCommand}
	}
	if opts.RepoPath == "" {
		return []string{skillScriptCommand}
	}
	return []string{"go test ./...", "go vet ./...", skillScriptCommand}
}

func changedRepoPaths(summary DiffSummary) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, file := range summary.Files {
		p := file.NewPath
		if p == "" {
			p = file.OldPath
		}
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func changedModuleDirs(repoPath string, changedFiles []string) ([]string, error) {
	if repoPath == "" || len(changedFiles) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, file := range changedFiles {
		modDir, err := nearestModuleDir(repoPath, file)
		if err != nil {
			return nil, err
		}
		if modDir == "" {
			continue
		}
		if _, ok := seen[modDir]; ok {
			continue
		}
		seen[modDir] = struct{}{}
		out = append(out, modDir)
	}
	sort.Strings(out)
	return out, nil
}

func nearestModuleDir(repoPath string, changedFile string) (string, error) {
	rel := filepath.FromSlash(changedFile)
	abs := filepath.Join(repoPath, rel)
	dir := abs
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		dir = filepath.Dir(abs)
	} else if err != nil {
		dir = filepath.Dir(abs)
	}
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			relDir, err := filepath.Rel(repoAbs, dir)
			if err != nil {
				return "", err
			}
			if relDir == "." {
				return ".", nil
			}
			return filepath.ToSlash(relDir), nil
		}
		if sameDir(dir, repoAbs) {
			return "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func sameDir(a string, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}

func diffSizeGate(summary DiffSummary, opts ReviewOptions) *Finding {
	maxLines := opts.MaxDiffLines
	if maxLines <= 0 {
		maxLines = defaultMaxDiffLines
	}
	maxFiles := opts.MaxChangedFiles
	if maxFiles <= 0 {
		maxFiles = defaultMaxChangedFiles
	}
	if summary.LineCount <= maxLines && len(summary.Files) <= maxFiles {
		return nil
	}
	return &Finding{
		Severity:       severityMedium,
		Category:       "input_gate",
		File:           "",
		Line:           0,
		Title:          "Diff exceeds deterministic review size gate",
		Evidence:       fmt.Sprintf("diff lines=%d/%d files=%d/%d", summary.LineCount, maxLines, len(summary.Files), maxFiles),
		Recommendation: "Split the change into smaller review units or raise the configured gate after human approval.",
		Confidence:     0.99,
		Source:         "deterministic-gate",
		RuleID:         "input.size_gate",
	}
}

func conclusion(findings []Finding, needsHuman []Finding, runs []SandboxRun) string {
	for _, run := range runs {
		if run.Status == "failed" {
			return "review completed with sandbox failures; inspect findings and sandbox summary"
		}
	}
	if len(findings) == 0 && len(needsHuman) == 0 {
		return "no high-confidence issues found"
	}
	if len(findings) > 0 {
		return "high-confidence issues found"
	}
	return "low-confidence issues need human review"
}
