//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a code review agent prototype CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	inputKindDiffFile = "diff-file"
	inputKindRepoPath = "repo-path"
	inputKindFixture  = "fixture"

	runtimeE2B   = "e2b"
	runtimeFake  = "fake"
	runtimeLocal = "local"

	defaultOutputDir = "output"
	defaultRuntime   = runtimeE2B

	envE2BTemplate = "TRPC_AGENT_CODE_REVIEW_E2B_TEMPLATE"

	maxDiffBytes   = int64(5 * 1024 * 1024)
	maxStderrBytes = int64(64 * 1024)
	gitDiffTimeout = 30 * time.Second
)

type gitCommandRunner func(context.Context, string, []string) ([]byte, []byte, error)

type config struct {
	diffFile          string
	repoPath          string
	files             repeatedStrings
	fixture           string
	showTask          string
	showTaskSet       bool
	dryRun            bool
	ruleOnly          bool
	runtime           string
	effectiveRuntime  string
	allowLocal        bool
	e2bTemplate       string
	skipGoTest        bool
	enableStaticcheck bool
	dbPath            string
	outputDir         string
	setFlags          map[string]bool
}

type repeatedStrings []string

func (s *repeatedStrings) String() string {
	return strings.Join(*s, ",")
}

func (s *repeatedStrings) Set(value string) error {
	parts := strings.Split(value, ",")
	*s = append(*s, parts...)
	return nil
}

type reviewInput struct {
	kind                     string
	source                   string
	diff                     []byte
	repoRoot                 string
	repoFiles                []string
	sandboxRepoRoot          string
	sandboxDiagnosticModules map[string]string
	fixture                  *fixtureItem
}

type reviewSummary struct {
	TaskID            string         `json:"task_id"`
	Status            string         `json:"status"`
	Stage             string         `json:"stage"`
	Failure           *reviewFailure `json:"failure,omitempty"`
	Conclusion        string         `json:"conclusion"`
	InputKind         string         `json:"input_kind"`
	Source            string         `json:"source"`
	DiffBytes         int            `json:"diff_bytes"`
	DiffSHA256        string         `json:"diff_sha256"`
	Runtime           string         `json:"runtime"`
	DryRun            bool           `json:"dry_run"`
	RuleOnly          bool           `json:"rule_only"`
	OutputDir         string         `json:"output_dir"`
	DBPath            string         `json:"db_path"`
	E2BTemplate       string         `json:"e2b_template,omitempty"`
	SkipGoTest        bool           `json:"skip_go_test"`
	EnableStaticcheck bool           `json:"enable_staticcheck"`
	ChangedFiles      int            `json:"changed_files"`
	Hunks             int            `json:"hunks"`
	CandidateLines    int            `json:"candidate_lines"`
	ParseWarnings     int            `json:"parse_warnings"`
	RuleMatches       int            `json:"rule_matches"`
	RuleWarnings      int            `json:"rule_warnings"`
	CommandsPlanned   int            `json:"commands_planned"`
	CommandsAllowed   int            `json:"commands_allowed"`
	CommandsBlocked   int            `json:"commands_blocked"`
	PermissionBlocks  int            `json:"permission_blocks"`
	Findings          int            `json:"findings"`
	Warnings          int            `json:"warnings"`
	NeedsHumanReview  bool           `json:"needs_human_review"`
	SuppressedMatches int            `json:"suppressed_matches"`
	Redactions        int            `json:"redactions"`
	FindingRuleIDs    []string       `json:"finding_rule_ids"`
	WarningRuleIDs    []string       `json:"warning_rule_ids"`
	SeverityCounts    map[string]int `json:"severity_counts"`
	ReportPaths       reportPaths    `json:"report_paths"`
	DurationMS        int64          `json:"duration_ms"`
}

type taskQueryError struct {
	Error  string `json:"error"`
	TaskID string `json:"task_id"`
	DBPath string `json:"db_path"`
}

type fixturesFile struct {
	Version  int                    `json:"version"`
	Fixtures map[string]fixtureItem `json:"fixtures"`
}

type fixtureItem struct {
	Description string               `json:"description"`
	Diff        string               `json:"diff"`
	Expected    *fixtureExpected     `json:"expected"`
	FakeSandbox fixtureSandboxConfig `json:"fake_sandbox,omitempty"`
}

type fixtureExpected struct {
	FindingRuleIDs *[]string `json:"finding_rule_ids"`
	WarningRuleIDs *[]string `json:"warning_rule_ids"`
}

type fixtureSandboxConfig struct {
	GoVersion   *fixtureSandboxRun `json:"go_version,omitempty"`
	Test        *fixtureSandboxRun `json:"test,omitempty"`
	Vet         *fixtureSandboxRun `json:"vet,omitempty"`
	Staticcheck *fixtureSandboxRun `json:"staticcheck,omitempty"`
}

type fixtureSandboxRun struct {
	ExitCode   int      `json:"exit_code"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	TimedOut   bool     `json:"timed_out,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Error      string   `json:"error,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, runGitCommand))
}

func run(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
	gitRunner gitCommandRunner,
) int {
	return runWithHooks(args, stdout, stderr, getenv, gitRunner, runtimeHooks{})
}

func runWithHooks(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
	gitRunner gitCommandRunner,
	hooks runtimeHooks,
) int {
	if getenv == nil {
		getenv = os.Getenv
	}
	if gitRunner == nil {
		gitRunner = runGitCommand
	}

	cfg, code, err := parseConfig(args, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return code
	}

	ctx := context.Background()
	if cfg.showTaskSet {
		store, ownsStore, err := openConfiguredReviewStore(ctx, cfg, hooks)
		if err != nil {
			response := taskQueryError{
				Error:  err.Error(),
				TaskID: cfg.showTask,
				DBPath: cfg.dbPath,
			}
			if writeErr := writeJSON(stdout, response); writeErr != nil {
				fmt.Fprintf(stderr, "error: write task query response: %v\n", writeErr)
			}
			return 1
		}
		if ownsStore {
			defer store.Close()
		}
		report, err := store.LoadReview(ctx, cfg.showTask)
		if err != nil {
			response := taskQueryError{
				Error:  err.Error(),
				TaskID: cfg.showTask,
				DBPath: cfg.dbPath,
			}
			if writeErr := writeJSON(stdout, response); writeErr != nil {
				fmt.Fprintf(stderr, "error: write task query response: %v\n", writeErr)
			}
			return 1
		}
		if err := writeJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "error: write task query response: %v\n", err)
			return 1
		}
		return 0
	}

	now := func() time.Time {
		if hooks.now != nil {
			return hooks.now().UTC()
		}
		return time.Now().UTC()
	}
	taskID := strings.TrimSpace(hooks.taskID)
	if taskID == "" {
		taskID = newTaskID()
	}
	started := now()
	store, ownsStore, err := openConfiguredReviewStore(ctx, cfg, hooks)
	if err != nil {
		writeReviewStageError(stderr, taskID, reviewStageStorageOpen, err)
		return 1
	}
	if ownsStore {
		defer store.Close()
	}
	report := newRunningReviewReport(taskID, cfg, started)
	if err := store.CheckpointReview(ctx, report); err != nil {
		writeReviewStageError(stderr, taskID, reviewStagePersistence, err)
		return 1
	}
	fail := func(stage string, cause error, rewriteFiles bool) int {
		markReviewFailed(&report, stage, cause, now())
		if rewriteFiles {
			_ = writeReviewReportFiles(&report, cfg.outputDir)
		}
		_ = store.CheckpointReview(ctx, report)
		writeReviewStageError(stderr, taskID, stage, errors.New(report.Failure.Message))
		return 1
	}

	input, err := loadReviewInput(ctx, cfg, gitRunner)
	if err != nil {
		return fail(reviewStageInput, err, false)
	}
	setReviewInputCheckpoint(&report, input)
	markReviewRunning(&report, reviewStageAnalysis, now())
	if err := store.CheckpointReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, err, false)
	}
	parsed := parseUnifiedDiff(input.diff)
	ruleMatches := runRules(parsed, input.repoRoot)
	highConfidenceRules, ruleWarnings := countRuleMatches(ruleMatches)
	markReviewRunning(&report, reviewStageGovernance, now())
	if err := store.CheckpointReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, err, false)
	}
	governance, err := runGovernance(ctx, cfg, input, parsed, hooks)
	if err != nil {
		return fail(reviewStageGovernance, err, false)
	}
	ruleMatches = append(ruleMatches, governance.Matches...)
	finalized := finalizeRuleMatches(ruleMatches)
	parseWarningMessages, parseWarningRedactions := redactParseWarningMessages(parsed.Warnings)

	report = buildReviewReport(
		taskID,
		cfg,
		input,
		parsed,
		governance,
		finalized,
		parseWarningMessages,
		highConfidenceRules,
		ruleWarnings,
		finalized.Redactions+parseWarningRedactions+governance.Redactions,
		started,
		started,
	)
	markReviewRunning(&report, reviewStageReportWrite, now())
	if err := store.CheckpointReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, err, false)
	}
	if err := writeReviewReportFiles(&report, cfg.outputDir); err != nil {
		return fail(reviewStageReportWrite, fmt.Errorf("write review report: %w", err), true)
	}
	markReviewRunning(&report, reviewStagePersistence, now())
	if err := store.CheckpointReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, err, true)
	}
	if err := store.SaveReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, fmt.Errorf("save review: %w", err), true)
	}
	markReviewCompleted(&report, now())
	if err := writeReviewReportFiles(&report, cfg.outputDir); err != nil {
		return fail(reviewStageReportWrite, fmt.Errorf("finalize review report: %w", err), true)
	}
	if err := store.SaveReview(ctx, report); err != nil {
		return fail(reviewStagePersistence, fmt.Errorf("finalize review persistence: %w", err), true)
	}
	response := report.summary()
	if err := writeJSON(stdout, response); err != nil {
		fmt.Fprintf(stderr, "error: write review summary: %v\n", err)
		return 1
	}
	return 0
}

func newTaskID() string {
	return "review-" + uuid.NewString()
}

func newRunningReviewReport(taskID string, cfg config, started time.Time) reviewReport {
	redactions := 0
	redact := func(value string) string {
		result := redactText(value)
		redactions += result.Count
		return result.Text
	}
	report := reviewReport{
		TaskID:     taskID,
		Status:     reviewStatusRunning,
		Stage:      reviewStageInput,
		Conclusion: reviewConclusionNeedsHumanReview,
		StartedAt:  started,
		FinishedAt: started,
		Runtime: reportRuntime{
			Runtime:           cfg.effectiveRuntime,
			DryRun:            cfg.dryRun,
			RuleOnly:          cfg.ruleOnly,
			E2BTemplate:       redact(cfg.e2bTemplate),
			SkipGoTest:        cfg.skipGoTest,
			EnableStaticcheck: cfg.enableStaticcheck,
			OutputDir:         redact(cfg.outputDir),
			DBPath:            redact(cfg.dbPath),
		},
		Rules: reportRules{
			NeedsHumanReview: true,
			SeverityCounts:   map[string]int{},
		},
	}
	report.Metrics = buildReportMetrics(report, redactions, 0)
	return report
}

func setReviewInputCheckpoint(report *reviewReport, input reviewInput) {
	source := redactText(input.source)
	report.Metrics.Redactions += source.Count
	report.Input = reportInput{
		Kind:       input.kind,
		Source:     source.Text,
		DiffBytes:  len(input.diff),
		DiffSHA256: diffSHA256(input.diff),
	}
}

func markReviewRunning(report *reviewReport, stage string, current time.Time) {
	report.Status = reviewStatusRunning
	report.Stage = stage
	report.Failure = nil
	report.Conclusion = reviewConclusionNeedsHumanReview
	setReviewTiming(report, current)
}

func markReviewCompleted(report *reviewReport, finished time.Time) {
	report.Status = reviewStatusCompleted
	report.Stage = reviewStageCompleted
	report.Failure = nil
	setReviewTiming(report, finished)
	report.Conclusion = determineConclusion(*report)
	report.Metrics = buildReportMetrics(*report, report.Metrics.Redactions, report.Metrics.ToolCalls)
}

func markReviewFailed(report *reviewReport, stage string, cause error, finished time.Time) {
	message := "review failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	redacted := redactText(message)
	report.Status = reviewStatusFailed
	report.Stage = stage
	report.Failure = &reviewFailure{Stage: stage, Message: redacted.Text}
	report.Conclusion = reviewConclusionNeedsHumanReview
	report.Rules.NeedsHumanReview = true
	setReviewTiming(report, finished)
	report.Metrics = buildReportMetrics(
		*report,
		report.Metrics.Redactions+redacted.Count,
		report.Metrics.ToolCalls,
	)
}

func setReviewTiming(report *reviewReport, finished time.Time) {
	report.FinishedAt = finished
	report.DurationMS = finished.Sub(report.StartedAt).Milliseconds()
	if report.DurationMS < 0 {
		report.DurationMS = 0
	}
	report.Metrics.TotalDurationMS = report.DurationMS
}

func writeReviewStageError(stderr io.Writer, taskID string, stage string, cause error) {
	message := "review failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	redacted := redactText(message)
	fmt.Fprintf(stderr, "error: task %s failed at %s: %s\n", taskID, stage, redacted.Text)
}

func parseConfig(args []string, getenv func(string) string) (config, int, error) {
	cfg := config{
		runtime:    defaultRuntime,
		skipGoTest: true,
		outputDir:  defaultOutputDir,
		dbPath:     filepath.Join(defaultOutputDir, "reviews.db"),
	}

	fs := flag.NewFlagSet("code_review_agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.diffFile, "diff-file", "", "path to a unified diff file")
	fs.StringVar(&cfg.repoPath, "repo-path", "", "path to a git repository to review")
	fs.Var(&cfg.files, "files", "changed files to include with --repo-path; repeat or comma-separate")
	fs.StringVar(&cfg.fixture, "fixture", "", "fixture name from testdata/fixtures.json")
	fs.StringVar(&cfg.showTask, "show-task", "", "task ID to query")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "use fake runtime and avoid external services")
	fs.BoolVar(&cfg.ruleOnly, "rule-only", false, "disable model advisory behavior")
	fs.StringVar(&cfg.runtime, "runtime", defaultRuntime, "sandbox runtime: e2b, fake, or local")
	fs.BoolVar(&cfg.allowLocal, "allow-local", false, "allow local runtime for development")
	fs.StringVar(&cfg.e2bTemplate, "e2b-template", "", "E2B template ID or alias")
	fs.BoolVar(&cfg.skipGoTest, "skip-go-test", true, "skip reviewed go test execution unless explicitly set to false")
	fs.BoolVar(&cfg.enableStaticcheck, "enable-staticcheck", false, "enable optional staticcheck command")
	fs.StringVar(&cfg.dbPath, "db-path", cfg.dbPath, "SQLite database path")
	fs.StringVar(&cfg.outputDir, "output-dir", cfg.outputDir, "directory for review outputs")

	if err := fs.Parse(args); err != nil {
		return cfg, 2, err
	}
	if fs.NArg() > 0 {
		return cfg, 2, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg.setFlags = map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		cfg.setFlags[f.Name] = true
	})
	cfg.showTaskSet = cfg.setFlags["show-task"]
	if cfg.setFlags["output-dir"] && !cfg.setFlags["db-path"] {
		cfg.dbPath = filepath.Join(cfg.outputDir, "reviews.db")
	}

	if cfg.e2bTemplate == "" {
		cfg.e2bTemplate = getenv(envE2BTemplate)
	}

	if err := cfg.validateRuntime(); err != nil {
		return cfg, 2, err
	}
	if err := cfg.validateMode(); err != nil {
		return cfg, 2, err
	}
	return cfg, 0, nil
}

func (cfg *config) validateRuntime() error {
	switch cfg.runtime {
	case runtimeE2B, runtimeFake, runtimeLocal:
	default:
		return fmt.Errorf("runtime must be one of e2b, fake, local")
	}
	if cfg.runtime == runtimeLocal && !cfg.allowLocal {
		return errors.New("local runtime requires --allow-local")
	}
	cfg.effectiveRuntime = cfg.runtime
	if cfg.dryRun {
		cfg.effectiveRuntime = runtimeFake
	}
	return nil
}

func (cfg *config) validateMode() error {
	if cfg.setFlags["diff-file"] && strings.TrimSpace(cfg.diffFile) == "" {
		return errors.New("--diff-file must not be empty")
	}
	if cfg.setFlags["repo-path"] && strings.TrimSpace(cfg.repoPath) == "" {
		return errors.New("--repo-path must not be empty")
	}
	if cfg.setFlags["fixture"] && strings.TrimSpace(cfg.fixture) == "" {
		return errors.New("--fixture must not be empty")
	}

	reviewInputs := 0
	for _, name := range []string{"diff-file", "repo-path", "fixture"} {
		if cfg.setFlags[name] {
			reviewInputs++
		}
	}

	if cfg.showTaskSet {
		if strings.TrimSpace(cfg.showTask) == "" {
			return errors.New("--show-task task ID must not be empty")
		}
		if reviewInputs > 0 || len(cfg.files) > 0 {
			return errors.New("--show-task cannot be combined with review input flags")
		}
		return nil
	}

	if len(cfg.files) > 0 && !cfg.setFlags["repo-path"] {
		return errors.New("--files can only be used with --repo-path")
	}
	if reviewInputs != 1 {
		return errors.New("review mode requires exactly one of --diff-file, --repo-path, or --fixture")
	}

	if len(cfg.files) > 0 {
		files, err := normalizeFileFilters(cfg.files)
		if err != nil {
			return err
		}
		cfg.files = files
	}
	return nil
}

func normalizeFileFilters(raw []string) (repeatedStrings, error) {
	normalized := make(repeatedStrings, 0, len(raw))
	for _, item := range raw {
		value := item
		if value == "" {
			return nil, errors.New("--files contains an empty path")
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("--files path %q contains a NUL byte", item)
		}

		value = strings.ReplaceAll(value, "\\", "/")
		if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
			hasWindowsDrive(value) {
			return nil, fmt.Errorf("--files path %q must be relative", item)
		}

		clean := path.Clean(value)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("--files path %q escapes the repository", item)
		}
		normalized = append(normalized, clean)
	}
	return normalized, nil
}

func hasWindowsDrive(value string) bool {
	return len(value) >= 2 &&
		((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':'
}

func loadReviewInput(
	ctx context.Context,
	cfg config,
	gitRunner gitCommandRunner,
) (reviewInput, error) {
	switch {
	case cfg.diffFile != "":
		diff, err := readLimitedFile(cfg.diffFile, maxDiffBytes)
		if err != nil {
			return reviewInput{}, fmt.Errorf("read diff file: %w", err)
		}
		return reviewInput{kind: inputKindDiffFile, source: cfg.diffFile, diff: diff}, nil
	case cfg.fixture != "":
		fixture, err := readFixture(cfg.fixture)
		if err != nil {
			return reviewInput{}, err
		}
		return reviewInput{
			kind:    inputKindFixture,
			source:  cfg.fixture,
			diff:    []byte(fixture.Diff),
			fixture: &fixture,
		}, nil
	case cfg.repoPath != "":
		runCtx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
		defer cancel()
		rootStdout, rootStderr, err := gitRunner(
			runCtx,
			cfg.repoPath,
			[]string{"rev-parse", "--show-toplevel"},
		)
		if err != nil {
			return reviewInput{}, gitCommandError("resolve git worktree root", err, rootStderr)
		}
		repoRoot, err := validateGitWorktreeRoot(cfg.repoPath, rootStdout)
		if err != nil {
			return reviewInput{}, err
		}
		args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--"}, []string(cfg.files)...)
		stdout, stderr, err := gitRunner(runCtx, repoRoot, args)
		if err != nil {
			return reviewInput{}, gitCommandError("run git diff", err, stderr)
		}
		if int64(len(stdout)) > maxDiffBytes {
			return reviewInput{}, fmt.Errorf("git diff output exceeds %d bytes", maxDiffBytes)
		}
		return reviewInput{
			kind:      inputKindRepoPath,
			source:    cfg.repoPath,
			diff:      stdout,
			repoRoot:  repoRoot,
			repoFiles: append([]string(nil), cfg.files...),
		}, nil
	default:
		return reviewInput{}, errors.New("no review input configured")
	}
}

func countRuleMatches(matches []ruleMatch) (int, int) {
	highConfidence := 0
	warnings := 0
	for _, match := range matches {
		if match.Confidence >= findingConfidenceThreshold {
			highConfidence++
			continue
		}
		warnings++
	}
	return highConfidence, warnings
}

func readFixture(name string) (fixtureItem, error) {
	data, err := readLimitedFile(fixturesPath(), maxDiffBytes)
	if err != nil {
		return fixtureItem{}, fmt.Errorf("read fixtures: %w", err)
	}
	fixtures, err := parseFixtures(data)
	if err != nil {
		return fixtureItem{}, err
	}
	fixture, ok := fixtures.Fixtures[name]
	if !ok {
		return fixtureItem{}, fmt.Errorf("fixture %q not found", name)
	}
	return fixture, nil
}

func parseFixtures(data []byte) (fixturesFile, error) {
	var fixtures fixturesFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return fixturesFile{}, fmt.Errorf("parse fixtures: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return fixturesFile{}, fmt.Errorf("parse fixtures: %w", err)
	}
	if fixtures.Version != 1 {
		return fixturesFile{}, fmt.Errorf("fixtures version %d is not supported", fixtures.Version)
	}
	for fixtureName, fixture := range fixtures.Fixtures {
		if err := validateFixture(fixtureName, fixture); err != nil {
			return fixturesFile{}, err
		}
	}
	return fixtures, nil
}

func validateFixture(name string, fixture fixtureItem) error {
	if fixture.Expected == nil {
		return fmt.Errorf("fixture %q is missing expected results", name)
	}
	if fixture.Expected.FindingRuleIDs == nil {
		return fmt.Errorf("fixture %q expected is missing finding_rule_ids", name)
	}
	if fixture.Expected.WarningRuleIDs == nil {
		return fmt.Errorf("fixture %q expected is missing warning_rule_ids", name)
	}
	if err := validateFixtureRuleIDs(name, "finding_rule_ids", *fixture.Expected.FindingRuleIDs); err != nil {
		return err
	}
	if err := validateFixtureRuleIDs(name, "warning_rule_ids", *fixture.Expected.WarningRuleIDs); err != nil {
		return err
	}
	for command, run := range map[string]*fixtureSandboxRun{
		"go_version":  fixture.FakeSandbox.GoVersion,
		"test":        fixture.FakeSandbox.Test,
		"vet":         fixture.FakeSandbox.Vet,
		"staticcheck": fixture.FakeSandbox.Staticcheck,
	} {
		if run == nil {
			continue
		}
		if run.ExitCode < -1 {
			return fmt.Errorf("fixture %q fake_sandbox.%s exit_code must be at least -1", name, command)
		}
		if run.DurationMS < 0 {
			return fmt.Errorf("fixture %q fake_sandbox.%s duration_ms must not be negative", name, command)
		}
		if run.Skipped && run.TimedOut {
			return fmt.Errorf("fixture %q fake_sandbox.%s cannot be both skipped and timed out", name, command)
		}
	}
	return nil
}

func validateFixtureRuleIDs(name string, field string, ruleIDs []string) error {
	seen := map[string]bool{}
	for _, ruleID := range ruleIDs {
		trimmed := strings.TrimSpace(ruleID)
		if trimmed == "" || trimmed != ruleID {
			return fmt.Errorf("fixture %q %s contains an empty or padded rule id", name, field)
		}
		if seen[ruleID] {
			return fmt.Errorf("fixture %q %s contains duplicate rule id %q", name, field, ruleID)
		}
		seen[ruleID] = true
	}
	return nil
}

func fixturesPath() string {
	candidates := []string{
		filepath.Join("testdata", "fixtures.json"),
		filepath.Join("code_review_agent", "testdata", "fixtures.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if ok {
		return filepath.Join(filepath.Dir(file), "testdata", "fixtures.json")
	}
	return filepath.Join("testdata", "fixtures.json")
}

func readLimitedFile(filePath string, limit int64) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(file, limit)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return buf.Bytes(), nil
}

type limitBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
