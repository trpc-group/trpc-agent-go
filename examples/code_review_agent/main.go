//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides a deterministic code review agent prototype.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diffparser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	reportwriter "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandboxrunner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

type config struct {
	diffFile    string
	files       string
	repoPath    string
	fixture     string
	taskID      string
	outDir      string
	dbPath      string
	mode        string
	sandboxKind string
	dryRun      bool
	timeout     time.Duration
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "code review agent failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	timeout := flag.Duration("timeout", 30*time.Second, "sandbox command timeout")
	cfg := config{}
	flag.StringVar(&cfg.diffFile, "diff-file", "", "path to a unified diff file")
	flag.StringVar(&cfg.files, "files", "", "comma-separated file path list to review")
	flag.StringVar(&cfg.repoPath, "repo-path", "", "repository path; git diff and optional checks run here")
	flag.StringVar(&cfg.fixture, "fixture", "", "fixture name or all")
	flag.StringVar(&cfg.taskID, "task-id", "", "query a persisted review task by id")
	flag.StringVar(&cfg.outDir, "out-dir", "code_review_output", "output directory")
	flag.StringVar(&cfg.dbPath, "db", "", "SQLite database path; defaults to out-dir/review.db")
	flag.StringVar(&cfg.mode, "mode", "rule-only", "rule-only|fake-model|llm")
	flag.StringVar(&cfg.sandboxKind, "sandbox", "mock", "mock|managed|container|e2b|local-dev")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "skip external command execution")
	flag.Parse()
	cfg.timeout = *timeout
	return cfg
}

func run(ctx context.Context, cfg config) error {
	if cfg.dbPath == "" {
		cfg.dbPath = filepath.Join(cfg.outDir, "review.db")
	}
	if cfg.taskID != "" {
		return queryTask(ctx, cfg)
	}
	if cfg.fixture == "all" {
		return runAllFixtures(ctx, cfg)
	}
	return runOne(ctx, cfg)
}

func queryTask(ctx context.Context, cfg config) error {
	db, err := store.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	snapshot, err := db.GetTask(ctx, cfg.taskID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(snapshot)
}

func runAllFixtures(ctx context.Context, cfg config) error {
	fixtures, err := filepath.Glob(filepath.Join("code_review_agent", "testdata", "fixtures", "*.diff"))
	if err != nil {
		return err
	}
	if len(fixtures) == 0 {
		return errors.New("no fixtures found")
	}
	for _, fixturePath := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixturePath), ".diff")
		next := cfg
		next.fixture = fixturePath
		next.diffFile = ""
		next.outDir = filepath.Join(cfg.outDir, name)
		if cfg.dbPath == filepath.Join(cfg.outDir, "review.db") {
			next.dbPath = filepath.Join(cfg.outDir, "review.db")
		}
		if err := runOne(ctx, next); err != nil {
			return fmt.Errorf("fixture %s: %w", name, err)
		}
	}
	return nil
}

func runOne(ctx context.Context, cfg config) error {
	start := time.Now()
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	diffData, inputType, inputSummary, err := loadInput(ctx, cfg)
	if err != nil {
		return err
	}
	files, err := diffparser.ParseUnifiedDiff(diffData)
	if err != nil {
		return err
	}
	task := review.ReviewTask{
		ID:           "cr-" + uuid.NewString(),
		Status:       review.StatusRunning,
		InputType:    inputType,
		InputSummary: redaction.RedactText(inputSummary),
		RepoPath:     cfg.repoPath,
		StartedAt:    start.UTC(),
	}
	if err := db.CreateTask(ctx, task); err != nil {
		return err
	}

	ruleResult := rules.Scan(files)
	redactedFiles := redactFiles(files)
	sandboxResult := sandboxrunner.RunChecks(ctx, sandboxrunner.Config{
		TaskID:      task.ID,
		RepoPath:    cfg.repoPath,
		SandboxKind: cfg.sandboxKind,
		DryRun:      cfg.dryRun,
		Timeout:     cfg.timeout,
	})
	finished := time.Now().UTC()
	task.Status = review.StatusCompleted
	task.FinishedAt = &finished

	report := review.ReviewReport{
		Task:                task,
		Files:               redactedFiles,
		Findings:            ruleResult.Findings,
		Warnings:            ruleResult.Warnings,
		NeedsHumanReview:    ruleResult.NeedsHumanReview,
		SandboxRuns:         sandboxResult.Runs,
		PermissionDecisions: sandboxResult.Decisions,
		Summary:             summary(ruleResult),
	}
	report.Metrics = buildMetrics(start, report)
	report.Artifacts = []review.Artifact{
		{Kind: "json_report", Path: filepath.Join(cfg.outDir, "review_report.json")},
		{Kind: "markdown_report", Path: filepath.Join(cfg.outDir, "review_report.md")},
	}

	artifacts, err := reportwriter.Write(cfg.outDir, report)
	if err != nil {
		task.Status = review.StatusFailed
		task.Error = err.Error()
		_ = db.FinishTask(ctx, task)
		return err
	}

	if err := db.SaveFindings(ctx, task.ID, append(append(ruleResult.Findings, ruleResult.Warnings...), ruleResult.NeedsHumanReview...)); err != nil {
		return err
	}
	if err := db.SaveSandboxRuns(ctx, task.ID, sandboxResult.Runs); err != nil {
		return err
	}
	if err := db.SavePermissionDecisions(ctx, task.ID, sandboxResult.Decisions); err != nil {
		return err
	}
	if err := db.SaveArtifacts(ctx, task.ID, artifacts); err != nil {
		return err
	}
	if err := db.SaveReport(ctx, task.ID, report, filepath.Join(cfg.outDir, "review_report.json"), filepath.Join(cfg.outDir, "review_report.md")); err != nil {
		return err
	}
	if err := db.FinishTask(ctx, task); err != nil {
		return err
	}
	fmt.Printf("task_id=%s\nreport_json=%s\nreport_md=%s\ndb=%s\n",
		task.ID,
		filepath.Join(cfg.outDir, "review_report.json"),
		filepath.Join(cfg.outDir, "review_report.md"),
		cfg.dbPath)
	return nil
}

func loadInput(ctx context.Context, cfg config) ([]byte, string, string, error) {
	switch {
	case cfg.fixture != "":
		path := cfg.fixture
		if !strings.HasSuffix(path, ".diff") {
			path = resolveFixturePath(path)
		}
		data, err := os.ReadFile(path)
		return data, review.InputTypeFixture, path, err
	case cfg.diffFile != "":
		data, err := os.ReadFile(cfg.diffFile)
		return data, review.InputTypeDiffFile, cfg.diffFile, err
	case cfg.files != "":
		data, summary, err := diffForFiles(cfg.repoPath, cfg.files)
		return data, review.InputTypeFiles, summary, err
	case cfg.repoPath != "":
		cmd := exec.CommandContext(ctx, "git", "-C", cfg.repoPath, "diff", "--no-ext-diff", "--unified=80")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil {
			return nil, "", "", fmt.Errorf("git diff: %w: %s", err, stderr.String())
		}
		return data, review.InputTypeRepoPath, cfg.repoPath, nil
	default:
		return nil, "", "", errors.New("set --diff-file, --repo-path, --files, --fixture, or --task-id")
	}
}

func resolveFixturePath(name string) string {
	candidates := []string{
		filepath.Join("code_review_agent", "testdata", "fixtures", name+".diff"),
		filepath.Join("testdata", "fixtures", name+".diff"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func diffForFiles(repoPath, filesValue string) ([]byte, string, error) {
	paths := splitFiles(filesValue)
	if len(paths) == 0 {
		return nil, "", errors.New("--files did not contain any paths")
	}
	var b strings.Builder
	var summaries []string
	for _, raw := range paths {
		hostPath := raw
		if repoPath != "" && !filepath.IsAbs(hostPath) {
			hostPath = filepath.Join(repoPath, hostPath)
		}
		data, err := os.ReadFile(hostPath)
		if err != nil {
			return nil, "", err
		}
		display := displayPath(repoPath, hostPath, raw)
		lines := strings.SplitAfter(string(data), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", display, display)
		fmt.Fprintf(&b, "--- a/%s\n", display)
		fmt.Fprintf(&b, "+++ b/%s\n", display)
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, line := range lines {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			b.WriteByte('+')
			b.WriteString(line)
			b.WriteByte('\n')
		}
		summaries = append(summaries, display)
	}
	return []byte(b.String()), strings.Join(summaries, ","), nil
}

func splitFiles(filesValue string) []string {
	parts := strings.FieldsFunc(filesValue, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t'
	})
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func displayPath(repoPath, hostPath, raw string) string {
	if repoPath != "" {
		if absRepo, err := filepath.Abs(repoPath); err == nil {
			if absHost, err := filepath.Abs(hostPath); err == nil {
				if rel, err := filepath.Rel(absRepo, absHost); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
					return filepath.ToSlash(rel)
				}
			}
		}
	}
	if !filepath.IsAbs(raw) {
		return filepath.ToSlash(raw)
	}
	return filepath.Base(raw)
}

func summary(result rules.Result) string {
	if len(result.Findings) == 0 && len(result.NeedsHumanReview) == 0 {
		return "No high-confidence findings were detected."
	}
	return fmt.Sprintf("%d high-confidence findings and %d human-review items detected.",
		len(result.Findings), len(result.NeedsHumanReview))
}

func buildMetrics(start time.Time, r review.ReviewReport) review.MetricsSummary {
	severity := map[string]int{}
	for _, f := range r.Findings {
		severity[f.Severity]++
	}
	exceptions := map[string]int{}
	var sandboxMS int64
	for _, run := range r.SandboxRuns {
		sandboxMS += run.DurationMS
		if run.Status == "failed" || run.Status == "timeout" {
			exceptions[run.Status]++
		}
	}
	denies := 0
	for _, d := range r.PermissionDecisions {
		if d.Decision != "allow" {
			denies++
		}
	}
	return review.MetricsSummary{
		TotalDurationMS:       time.Since(start).Milliseconds(),
		SandboxDurationMS:     sandboxMS,
		ToolCallCount:         len(r.SandboxRuns),
		PermissionDenyCount:   denies,
		FindingCount:          len(r.Findings),
		WarningCount:          len(r.Warnings),
		NeedsHumanReviewCount: len(r.NeedsHumanReview),
		SeverityCounts:        severity,
		ExceptionCounts:       exceptions,
	}
}

func redactFiles(files []review.ChangedFile) []review.ChangedFile {
	out := make([]review.ChangedFile, len(files))
	copy(out, files)
	for i := range out {
		out[i].Hunks = make([]review.Hunk, len(files[i].Hunks))
		copy(out[i].Hunks, files[i].Hunks)
		for j := range out[i].Hunks {
			out[i].Hunks[j].Lines = make([]review.DiffLine, len(files[i].Hunks[j].Lines))
			copy(out[i].Hunks[j].Lines, files[i].Hunks[j].Lines)
			for k := range out[i].Hunks[j].Lines {
				out[i].Hunks[j].Lines[k].Content = redaction.RedactText(out[i].Hunks[j].Lines[k].Content)
			}
		}
	}
	return out
}
