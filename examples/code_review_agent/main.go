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
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diffparser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	reportwriter "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/reviewagent"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandboxrunner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/skillrunner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

type config struct {
	diffFile          string
	files             string
	repoPath          string
	fixture           string
	taskID            string
	outDir            string
	dbPath            string
	mode              string
	modelName         string
	sandboxKind       string
	skillsRoot        string
	telemetryEndpoint string
	dryRun            bool
	staticcheck       bool
	timeout           time.Duration
}

// main is the CLI entry point for the code review agent example.
func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "code review agent failed: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags builds the CLI configuration from command-line flags.
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
	flag.StringVar(&cfg.modelName, "model", defaultModelName(), "model name for --mode llm")
	flag.StringVar(&cfg.sandboxKind, "sandbox", "managed", "managed|container|e2b|mock|local-dev")
	flag.StringVar(&cfg.skillsRoot, "skills-root", "", "skills root directory; defaults to the bundled skills folder")
	flag.StringVar(&cfg.telemetryEndpoint, "telemetry-endpoint", "", "OTLP gRPC endpoint for trace export; empty keeps the no-op tracer")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "skip external command execution")
	flag.BoolVar(&cfg.staticcheck, "staticcheck", false, "also run staticcheck ./... in the sandbox when available")
	flag.Parse()
	cfg.timeout = *timeout
	return cfg
}

// run dispatches the CLI into query, all-fixtures, or single-review mode.
func run(ctx context.Context, cfg config) error {
	if err := validateMode(cfg.mode); err != nil {
		return err
	}
	if cfg.telemetryEndpoint != "" {
		clean, err := atrace.Start(ctx,
			atrace.WithEndpoint(cfg.telemetryEndpoint),
			atrace.WithServiceName("code-review-agent"))
		if err != nil {
			return fmt.Errorf("start telemetry: %w", err)
		}
		defer func() {
			if err := clean(); err != nil {
				fmt.Fprintf(os.Stderr, "telemetry shutdown: %v\n", err)
			}
		}()
	}
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

// validateMode rejects unknown model modes early.
func validateMode(mode string) error {
	switch mode {
	case "rule-only", reviewagent.ModeFakeModel, reviewagent.ModeLLM:
		return nil
	default:
		return fmt.Errorf("unsupported --mode %q; use rule-only, fake-model, or llm", mode)
	}
}

// defaultModelName picks the model from MODEL_NAME with a sane default.
func defaultModelName() string {
	if name := os.Getenv("MODEL_NAME"); name != "" {
		return name
	}
	return "deepseek-v4-flash"
}

// queryTask prints the stored snapshot of a previous review task.
func queryTask(ctx context.Context, cfg config) error {
	db, err := openStore(ctx, cfg.dbPath)
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

// openStore opens the persistence backend behind the store.Store
// interface so another SQL implementation can be swapped in here.
func openStore(ctx context.Context, path string) (store.Store, error) {
	return store.Open(ctx, path)
}

// runAllFixtures reviews every bundled fixture in sequence.
func runAllFixtures(ctx context.Context, cfg config) error {
	fixtures, err := filepath.Glob(filepath.Join(fixturesDir(), "*.diff"))
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

// runOne executes a full review for one input and persists the results.
func runOne(ctx context.Context, cfg config) (err error) {
	start := time.Now()
	ctx, span := atrace.Tracer.Start(ctx, "code_review.run")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		return err
	}
	db, err := openStore(ctx, cfg.dbPath)
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
	modelOut, modelErr := runModelReview(ctx, cfg, task.ID, redactedFiles)
	ruleResult = rules.Merge(ruleResult, modelOut.Findings)
	sandboxResult := sandboxrunner.RunChecks(ctx, sandboxrunner.Config{
		TaskID:            task.ID,
		RepoPath:          cfg.repoPath,
		SandboxKind:       cfg.sandboxKind,
		DryRun:            cfg.dryRun,
		EnableStaticcheck: cfg.staticcheck,
		Timeout:           cfg.timeout,
	})
	skillResult := runSkillScripts(ctx, cfg, task.ID, string(diffData))
	allRuns := append(sandboxResult.Runs, skillResult.Runs...)
	allDecisions := append(sandboxResult.Decisions, skillResult.Decisions...)
	finished := time.Now().UTC()
	task.Status = review.StatusCompleted
	task.FinishedAt = &finished

	report := review.ReviewReport{
		Task:                task,
		Files:               redactedFiles,
		Findings:            ruleResult.Findings,
		Warnings:            ruleResult.Warnings,
		NeedsHumanReview:    ruleResult.NeedsHumanReview,
		FilterDecisions:     ruleResult.FilterDecisions,
		SandboxRuns:         allRuns,
		PermissionDecisions: allDecisions,
		Summary:             summary(ruleResult, modelOut, modelErr),
	}
	report.Metrics = buildMetrics(start, report)
	report.Metrics.ModelCallCount = modelOut.ModelCalls
	report.Metrics.ModelDurationMS = modelOut.DurationMS
	if modelErr != nil {
		report.Metrics.ExceptionCounts["model_error"]++
	}
	if skillResult.Err != nil {
		report.Metrics.ExceptionCounts["skill_error"]++
	}
	span.SetAttributes(
		attribute.String("code_review.task_id", task.ID),
		attribute.String("code_review.mode", cfg.mode),
		attribute.String("code_review.sandbox", cfg.sandboxKind),
		attribute.Int("code_review.finding_count", len(report.Findings)),
		attribute.Int("code_review.warning_count", len(report.Warnings)),
		attribute.Int("code_review.human_review_count", len(report.NeedsHumanReview)),
		attribute.Int("code_review.filter_decision_count", len(report.FilterDecisions)),
		attribute.Int("code_review.sandbox_run_count", len(allRuns)),
		attribute.Int("code_review.permission_deny_count", report.Metrics.PermissionDenyCount),
	)
	for decision, count := range report.Metrics.FilterDecisionCounts {
		span.SetAttributes(attribute.Int("code_review.filter."+decision, count))
	}
	report.Artifacts = []review.Artifact{
		{Kind: "json_report", Path: filepath.Join(cfg.outDir, "review_report.json")},
		{Kind: "markdown_report", Path: filepath.Join(cfg.outDir, "review_report.md")},
	}

	// fail finalizes the task so persistence errors never leave it in
	// the "running" state without an audit trail.
	fail := func(err error) error {
		task.Status = review.StatusFailed
		task.Error = err.Error()
		_ = db.FinishTask(ctx, task)
		return err
	}

	artifacts, err := reportwriter.Write(cfg.outDir, report)
	if err != nil {
		return fail(err)
	}

	if err := db.SaveFindings(ctx, task.ID, append(append(ruleResult.Findings, ruleResult.Warnings...), ruleResult.NeedsHumanReview...)); err != nil {
		return fail(err)
	}
	if err := db.SaveSandboxRuns(ctx, task.ID, allRuns); err != nil {
		return fail(err)
	}
	if err := db.SavePermissionDecisions(ctx, task.ID, allDecisions); err != nil {
		return fail(err)
	}
	if err := db.SaveFilterDecisions(ctx, task.ID, ruleResult.FilterDecisions); err != nil {
		return fail(err)
	}
	if err := db.SaveArtifacts(ctx, task.ID, artifacts); err != nil {
		return fail(err)
	}
	if err := db.SaveReport(ctx, task.ID, report, filepath.Join(cfg.outDir, "review_report.json"), filepath.Join(cfg.outDir, "review_report.md")); err != nil {
		return fail(err)
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

// loadInput resolves the diff bytes and labels from the configured source.
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

// fixturesDir locates the bundled fixtures whether the binary runs from
// the examples root, the code_review_agent directory, or any other
// working directory (via the compile-time source location).
func fixturesDir() string {
	return firstExistingPath(
		filepath.Join("code_review_agent", "testdata", "fixtures"),
		filepath.Join("testdata", "fixtures"),
		filepath.Join(exampleDir(), "testdata", "fixtures"),
	)
}

// resolveFixturePath locates a bundled fixture independent of the working dir.
func resolveFixturePath(name string) string {
	return firstExistingPath(
		filepath.Join("code_review_agent", "testdata", "fixtures", name+".diff"),
		filepath.Join("testdata", "fixtures", name+".diff"),
		filepath.Join(exampleDir(), "testdata", "fixtures", name+".diff"),
	)
}

// resolveSkillsRoot locates the bundled skills directory when
// --skills-root is not set.
func resolveSkillsRoot(root string) string {
	if root != "" {
		return root
	}
	return firstExistingPath(
		filepath.Join("code_review_agent", "skills"),
		"skills",
		filepath.Join(exampleDir(), "skills"),
	)
}

// exampleDir returns the directory of this source file so bundled
// fixtures and skills resolve regardless of the working directory.
func exampleDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(file)
}

// firstExistingPath returns the first candidate that exists on disk,
// falling back to the first candidate for error reporting.
func firstExistingPath(candidates ...string) string {
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

// runSkillScripts loads the code-review skill via tool/skill and runs
// its scripts in the sandbox. Errors degrade to audit records only.
func runSkillScripts(ctx context.Context, cfg config, taskID string, diffText string) skillrunner.Result {
	result := skillrunner.RunScripts(ctx, skillrunner.Config{
		TaskID:      taskID,
		SkillsRoot:  resolveSkillsRoot(cfg.skillsRoot),
		RepoPath:    cfg.repoPath,
		SandboxKind: cfg.sandboxKind,
		DryRun:      cfg.dryRun,
		Timeout:     cfg.timeout,
		DiffText:    diffText,
	})
	if result.Err != nil {
		fmt.Fprintf(os.Stderr, "skill scripts degraded: %v\n", result.Err)
	}
	return result
}

// diffForFiles synthesizes a unified diff for an explicit file list.
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

// splitFiles splits the comma-separated --files value into paths.
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

// displayPath prefers a repo-relative path when rendering file names.
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

// summary produces the one-line result description for the report.
func summary(result rules.Result, modelOut reviewagent.Output, modelErr error) string {
	var b strings.Builder
	if len(result.Findings) == 0 && len(result.NeedsHumanReview) == 0 {
		b.WriteString("No high-confidence findings were detected.")
	} else {
		fmt.Fprintf(&b, "%d high-confidence findings and %d human-review items detected.",
			len(result.Findings), len(result.NeedsHumanReview))
	}
	if modelOut.Summary != "" {
		b.WriteString(" Model review: ")
		b.WriteString(modelOut.Summary)
	}
	if modelErr != nil {
		b.WriteString(" Model review failed and was skipped: ")
		b.WriteString(redaction.RedactText(modelErr.Error()))
	}
	return b.String()
}

// runModelReview runs the model-assisted pass for fake-model and llm modes.
// Errors degrade the review to rule-only results instead of failing the task.
func runModelReview(ctx context.Context, cfg config, taskID string, redactedFiles []review.ChangedFile) (reviewagent.Output, error) {
	if cfg.mode == "rule-only" {
		return reviewagent.Output{}, nil
	}
	out, err := reviewagent.Review(ctx, reviewagent.Config{
		Mode:      cfg.mode,
		ModelName: cfg.modelName,
		TaskID:    taskID,
		Timeout:   cfg.timeout,
	}, redactedFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model review degraded to rule-only: %v\n", err)
	}
	return out, err
}

// buildMetrics aggregates the monitoring summary from the final report.
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
	filterCounts := map[string]int{}
	for _, d := range r.FilterDecisions {
		filterCounts[d.Decision]++
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
		FilterDecisionCounts:  filterCounts,
	}
}

// redactFiles redacts changed-file contents before persistence.
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
