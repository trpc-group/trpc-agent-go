//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/safetywrap"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandboxrun"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

const (
	defaultMaxSandboxOutput = 4096
	defaultSandboxCommand   = "go test ./..."
)

// Options configures one review run.
type Options struct {
	FixtureDir string
	OutDir     string
	DBPath     string
	Model      string
	Runtime    string
	RepoPath   string
	Now        time.Time
}

// Result is returned by the orchestrator after reports are written.
type Result struct {
	TaskID       string
	Report       review.Report
	JSONPath     string
	MarkdownPath string
	DBPath       string
}

// Run executes a deterministic review over fixture diffs.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.FixtureDir == "" {
		opts.FixtureDir = "testdata/fixtures"
	}
	if opts.OutDir == "" {
		opts.OutDir = "./out"
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(opts.OutDir, "review_agent.db")
	}
	if opts.Runtime == "" {
		opts.Runtime = "container"
	}
	fixedNow := !opts.Now.IsZero()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	rawDiff, fixtureNames, err := readFixtures(opts.FixtureDir)
	if err != nil {
		return Result{}, err
	}
	taskID := stableTaskID(rawDiff, now)
	task := review.ReviewTask{
		ID:        taskID,
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		RepoPath:  opts.RepoPath,
		DiffHash:  hashText(rawDiff),
		StartedAt: now.UTC(),
	}

	st, err := store.NewSQLite(ctx, opts.DBPath)
	if err != nil {
		return Result{}, err
	}
	defer st.Close()
	if err := st.CreateTask(ctx, task); err != nil {
		return Result{}, err
	}

	files, err := diffparse.Parse(rawDiff)
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, err
	}
	changedFilesJSON, err := json.Marshal(files)
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, fmt.Errorf("marshal changed files: %w", err)
	}
	redactedDiff := redact.Text(rawDiff)
	if err := st.RecordInput(ctx, store.InputRecord{
		TaskID:           task.ID,
		DiffSummary:      summarizeDiff(files, fixtureNames),
		ChangedFilesJSON: string(changedFilesJSON),
		RedactedDiff:     redactedDiff.Text,
	}); err != nil {
		return Result{}, err
	}

	findings := rules.Evaluate(files)
	if err := st.SaveFindings(ctx, task.ID, findings); err != nil {
		return Result{}, err
	}

	decision := safetywrap.Decide(safetywrap.PlannedCommand{
		ID:       task.ID + "-permission-001",
		TaskID:   task.ID,
		ToolName: "workspace_exec",
		Command:  defaultSandboxCommand,
		Now:      now,
	})
	if err := st.RecordPermissionDecision(ctx, decision); err != nil {
		return Result{}, err
	}

	var runs []review.SandboxRun
	if decision.Blocked {
		runs = append(runs, review.SandboxRun{
			ID:             task.ID + "-sandbox-001",
			TaskID:         task.ID,
			Runtime:        opts.Runtime,
			Command:        defaultSandboxCommand,
			Status:         sandboxrun.StatusSkipped,
			DurationMillis: 0,
			ErrorType:      sandboxrun.ErrorPermissionBlocked,
		})
	} else {
		runs = append(runs, sandboxrun.Run(ctx, runtimeForName(opts.Runtime), task.ID, task.ID+"-sandbox-001", defaultSandboxCommand, defaultMaxSandboxOutput))
	}
	for _, run := range runs {
		if err := st.RecordSandboxRun(ctx, run); err != nil {
			return Result{}, err
		}
	}

	metrics := report.BuildMetrics(task.ID, task.StartedAt, findings, runs, []review.PermissionDecisionRecord{decision}, redactedDiff.Count+countFindingRedactions(findings))
	if fixedNow {
		metrics.TotalDurationMillis = 0
	}
	task.Status = statusFor(findings, runs)
	task.FinishedAt = now.UTC()
	conclusion := conclusionFor(task.Status, findings, runs)
	r := review.Report{
		Task:                task,
		Summary:             summarizeOutcome(files, findings, runs),
		ChangedFiles:        files,
		Findings:            findings,
		SandboxRuns:         runs,
		PermissionDecisions: []review.PermissionDecisionRecord{decision},
		Metrics:             metrics,
		Conclusion:          conclusion,
	}
	artifacts, err := report.Write(opts.OutDir, r, now)
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, err
	}
	r.Artifacts = artifacts
	if err := st.SaveArtifacts(ctx, artifacts); err != nil {
		return Result{}, err
	}
	jsonPath, mdPath := artifactPaths(artifacts)
	metricsJSON, _ := json.Marshal(metrics)
	if err := st.SaveReport(ctx, store.ReportRecord{
		TaskID:       task.ID,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		Conclusion:   conclusion,
		MetricsJSON:  string(metricsJSON),
	}); err != nil {
		return Result{}, err
	}
	if err := st.FinishTask(ctx, task.ID, task.Status, ""); err != nil {
		return Result{}, err
	}
	return Result{
		TaskID:       task.ID,
		Report:       r,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		DBPath:       opts.DBPath,
	}, nil
}

func readFixtures(dir string) (string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("read fixture dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".diff") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", nil, fmt.Errorf("read fixture %s: %w", name, err)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), names, nil
}

func stableTaskID(diff string, now time.Time) string {
	sum := sha256.Sum256([]byte(diff + now.UTC().Format("20060102")))
	return "review-" + hex.EncodeToString(sum[:])[:12]
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func summarizeDiff(files []review.DiffFile, fixtureNames []string) string {
	return fmt.Sprintf("Reviewed %d diff fixtures across %d changed files.", len(fixtureNames), len(files))
}

func summarizeOutcome(files []review.DiffFile, findings []review.Finding, runs []review.SandboxRun) string {
	return fmt.Sprintf("Reviewed %d changed files, produced %d findings, and recorded %d sandbox runs.", len(files), len(findings), len(runs))
}

func countFindingRedactions(findings []review.Finding) int {
	count := 0
	for _, finding := range findings {
		count += redact.Text(finding.Evidence).Count
		count += redact.Text(finding.Recommendation).Count
	}
	return count
}

func runtimeForName(name string) sandboxrun.Runtime {
	return sandboxrun.FakeRuntime{RuntimeName: name}
}

func statusFor(findings []review.Finding, runs []review.SandboxRun) string {
	for _, run := range runs {
		if run.Status == sandboxrun.StatusFailed || run.Status == sandboxrun.StatusUnavailable {
			return review.TaskStatusFailed
		}
	}
	for _, finding := range findings {
		if finding.Status == review.FindingStatusNeedsHumanReview {
			return review.TaskStatusFailed
		}
	}
	return review.TaskStatusPassed
}

func conclusionFor(status string, findings []review.Finding, runs []review.SandboxRun) string {
	if status == review.TaskStatusFailed {
		return "needs_human_review"
	}
	if len(findings) > 0 {
		return "findings_recorded"
	}
	if len(runs) == 0 {
		return "no_sandbox_run"
	}
	return "passed"
}

func artifactPaths(artifacts []review.ArtifactRecord) (string, string) {
	var jsonPath, mdPath string
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "json_report":
			jsonPath = artifact.Path
		case "markdown_report":
			mdPath = artifact.Path
		}
	}
	return jsonPath, mdPath
}
