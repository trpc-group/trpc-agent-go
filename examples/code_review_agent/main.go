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
	"os/exec"
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

type gitDiffRunner func(context.Context, string, []string) ([]byte, []byte, error)

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
	kind     string
	source   string
	diff     []byte
	repoRoot string
}

type reviewSummary struct {
	TaskID            string         `json:"task_id"`
	Status            string         `json:"status"`
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
	Description string `json:"description"`
	Diff        string `json:"diff"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, runGitDiff))
}

func run(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
	gitRunner gitDiffRunner,
) int {
	return runWithHooks(args, stdout, stderr, getenv, gitRunner, runtimeHooks{})
}

func runWithHooks(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
	gitRunner gitDiffRunner,
	hooks runtimeHooks,
) int {
	if getenv == nil {
		getenv = os.Getenv
	}
	if gitRunner == nil {
		gitRunner = runGitDiff
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

	started := time.Now().UTC()
	input, err := loadReviewInput(ctx, cfg, gitRunner)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	parsed := parseUnifiedDiff(input.diff)
	ruleMatches := runRules(parsed, input.repoRoot)
	highConfidenceRules, ruleWarnings := countRuleMatches(ruleMatches)
	governance, err := runGovernance(ctx, cfg, input, parsed, hooks)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ruleMatches = append(ruleMatches, governance.Warnings...)
	finalized := finalizeRuleMatches(ruleMatches)
	parseWarningMessages, parseWarningRedactions := redactParseWarningMessages(parsed.Warnings)

	taskID := strings.TrimSpace(hooks.taskID)
	if taskID == "" {
		taskID = newTaskID()
	}
	finished := time.Now().UTC()
	report := buildReviewReport(
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
		finished,
	)
	if err := writeReviewReportFiles(&report, cfg.outputDir); err != nil {
		fmt.Fprintf(stderr, "error: write review report: %v\n", err)
		return 1
	}
	store, ownsStore, err := openConfiguredReviewStore(ctx, cfg, hooks)
	if err != nil {
		fmt.Fprintf(stderr, "error: open review store: %v\n", err)
		return 1
	}
	if ownsStore {
		defer store.Close()
	}
	if err := store.SaveReview(ctx, report); err != nil {
		fmt.Fprintf(stderr, "error: save review: %v\n", err)
		return 1
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

func parseConfig(args []string, getenv func(string) string) (config, int, error) {
	cfg := config{
		runtime:   defaultRuntime,
		outputDir: defaultOutputDir,
		dbPath:    filepath.Join(defaultOutputDir, "reviews.db"),
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
		value := strings.TrimSpace(item)
		if value == "" {
			return nil, errors.New("--files contains an empty path")
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("--files path %q contains a NUL byte", value)
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
	gitRunner gitDiffRunner,
) (reviewInput, error) {
	switch {
	case cfg.diffFile != "":
		diff, err := readLimitedFile(cfg.diffFile, maxDiffBytes)
		if err != nil {
			return reviewInput{}, fmt.Errorf("read diff file: %w", err)
		}
		return reviewInput{kind: inputKindDiffFile, source: cfg.diffFile, diff: diff}, nil
	case cfg.fixture != "":
		diff, err := readFixture(cfg.fixture)
		if err != nil {
			return reviewInput{}, err
		}
		return reviewInput{kind: inputKindFixture, source: cfg.fixture, diff: diff}, nil
	case cfg.repoPath != "":
		runCtx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
		defer cancel()
		args := append([]string{"diff", "HEAD", "--"}, []string(cfg.files)...)
		stdout, stderr, err := gitRunner(runCtx, cfg.repoPath, args)
		if err != nil {
			msg := strings.TrimSpace(string(stderr))
			if msg == "" {
				return reviewInput{}, fmt.Errorf("run git diff: %w", err)
			}
			return reviewInput{}, fmt.Errorf("run git diff: %w: %s", err, msg)
		}
		if int64(len(stdout)) > maxDiffBytes {
			return reviewInput{}, fmt.Errorf("git diff output exceeds %d bytes", maxDiffBytes)
		}
		return reviewInput{
			kind:     inputKindRepoPath,
			source:   cfg.repoPath,
			diff:     stdout,
			repoRoot: cfg.repoPath,
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

func readFixture(name string) ([]byte, error) {
	data, err := readLimitedFile(fixturesPath(), maxDiffBytes)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	var fixtures fixturesFile
	if err := json.Unmarshal(data, &fixtures); err != nil {
		return nil, fmt.Errorf("parse fixtures: %w", err)
	}
	if fixtures.Version != 1 {
		return nil, fmt.Errorf("fixtures version %d is not supported", fixtures.Version)
	}
	fixture, ok := fixtures.Fixtures[name]
	if !ok {
		return nil, fmt.Errorf("fixture %q not found", name)
	}
	return []byte(fixture.Diff), nil
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

func runGitDiff(ctx context.Context, repoPath string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	var stdout limitBuffer
	var stderr limitBuffer
	stdout.limit = int(maxDiffBytes)
	stderr.limit = int(maxStderrBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("git diff timed out after %s", gitDiffTimeout)
	}
	if stdout.truncated {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("git diff output exceeds %d bytes", maxDiffBytes)
	}
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
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
