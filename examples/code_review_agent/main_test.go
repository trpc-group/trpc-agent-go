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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// TestFixtureReportsExpectedRules verifies each fixture triggers its rule IDs.
func TestFixtureReportsExpectedRules(t *testing.T) {
	expected := map[string][]string{
		"security_secret":        {"SEC001"},
		"goroutine_context_leak": {"GOR001"},
		"resource_not_closed":    {"RES001"},
		"db_lifecycle":           {"DB001"},
		"missing_test":           {"TEST001"},
		"duplicate_findings":     {"ERR001"},
		"redaction":              {"SEC001"},
	}
	outDir := filepath.Join(t.TempDir(), "out")
	dbPath := filepath.Join(t.TempDir(), "review.db")
	for fixture, wantRules := range expected {
		cfg := config{
			fixture:     fixture,
			outDir:      filepath.Join(outDir, fixture),
			dbPath:      dbPath,
			mode:        "rule-only",
			sandboxKind: "mock",
			dryRun:      true,
			timeout:     time.Second,
		}
		if err := run(context.Background(), cfg); err != nil {
			t.Fatalf("run fixture %s: %v", fixture, err)
		}
		report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
		for _, ruleID := range wantRules {
			if !hasRule(report, ruleID) {
				t.Fatalf("fixture %s missing rule %s", fixture, ruleID)
			}
		}
		assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
		assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.md"))
	}
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	report := readReport(t, filepath.Join(outDir, "security_secret", "review_report.json"))
	snapshot, err := db.GetTask(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.ID != report.Task.ID || len(snapshot.Findings) == 0 {
		t.Fatalf("bad snapshot: task=%q findings=%d", snapshot.Task.ID, len(snapshot.Findings))
	}
}

// TestFilesInputBuildsReview verifies --files input produces a full review.
func TestFilesInputBuildsReview(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(src, []byte(`package demo

var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		files:       "secret.go",
		repoPath:    dir,
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if report.Task.InputType != review.InputTypeFiles {
		t.Fatalf("input type=%s", report.Task.InputType)
	}
	if !hasRule(report, "SEC001") {
		t.Fatal("files input did not detect SEC001")
	}
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
}

func TestDiffForRepoIncludesStagedAndUntrackedFiles(t *testing.T) {
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repoDir, "staged.go"),
		[]byte("package sample\n\nconst staged = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "staged.go")
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.go"),
		[]byte("package sample\n\nconst untracked = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, _, err := diffForRepo(context.Background(), repoDir)
	if err != nil {
		t.Fatal(err)
	}
	text := string(diff)
	for _, want := range []string{"staged.go", "untracked.go", "const staged = true", "const untracked = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("combined diff missing %q:\n%s", want, text)
		}
	}
}

// TestFakeModelModeRunsAgentChain verifies fake-model mode reaches the agent chain.
func TestFakeModelModeRunsAgentChain(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		fixture:     "security_secret",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "fake-model",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     5 * time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("fake-model run failed: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if !hasRule(report, "SEC001") {
		t.Fatal("rule findings missing in fake-model mode")
	}
	if !hasRule(report, "FAKE001") {
		t.Fatal("fake model finding missing; agent chain did not run")
	}
	if report.Metrics.ModelCallCount != 1 {
		t.Fatalf("model call count = %d, want 1", report.Metrics.ModelCallCount)
	}
	if !strings.Contains(report.Summary, "fake-model review") {
		t.Fatalf("summary missing model review note: %q", report.Summary)
	}
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.md"))
}

// TestLLMModeFailureDegradesToRuleOnly verifies model errors keep rule results.
func TestLLMModeFailureDegradesToRuleOnly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	cfg := config{
		fixture:     "security_secret",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "llm",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     5 * time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("llm-mode failure must not crash the review: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if !hasRule(report, "SEC001") {
		t.Fatal("rule findings missing after model failure")
	}
	if report.Metrics.ExceptionCounts["model_error"] != 1 {
		t.Fatalf("model_error not recorded: %+v", report.Metrics.ExceptionCounts)
	}
	if !strings.Contains(report.Summary, "Model review failed") {
		t.Fatalf("summary missing degradation note: %q", report.Summary)
	}
}

// TestDefaultModelName verifies the dedicated setting wins while MODEL_NAME
// remains supported for compatibility with other examples.
func TestDefaultModelName(t *testing.T) {
	t.Setenv("TRPC_AGENT_MODEL", " trpc-model ")
	t.Setenv("MODEL_NAME", "legacy-model")
	if got := defaultModelName(); got != "trpc-model" {
		t.Fatalf("default model = %q, want trpc-model", got)
	}
	t.Setenv("TRPC_AGENT_MODEL", "")
	if got := defaultModelName(); got != "legacy-model" {
		t.Fatalf("compatibility model = %q, want legacy-model", got)
	}
	t.Setenv("MODEL_NAME", "")
	if got := defaultModelName(); got != "gpt-4o-mini" {
		t.Fatalf("fallback model = %q, want gpt-4o-mini", got)
	}
}

// TestLLMModeOpenAICompatibleEndpoint verifies the real model path uses the
// configured endpoint, redacts input, parses the response, and records metrics.
func TestLLMModeOpenAICompatibleEndpoint(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var err error
		requestBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		content := `{"summary":"model endpoint review succeeded","findings":[` +
			`{"severity":"high","category":"security","file":"security/secret.go",` +
			`"line":3,"title":"Avoid committed credentials","evidence":"redacted credential",` +
			`"recommendation":"Load the value from a secret store","confidence":0.95,` +
			`"rule_id":"LLM-SECRET"}]}`
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1,
			"model": "test-model", "choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": content},
				"finish_reason": "stop",
			}},
		}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	dir := t.TempDir()
	cfg := config{
		fixture: "security_secret", outDir: filepath.Join(dir, "out"),
		dbPath: filepath.Join(dir, "review.db"), mode: "llm", modelName: "test-model",
		sandboxKind: "mock", dryRun: true, timeout: 5 * time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("llm endpoint run failed: %v", err)
	}
	for _, secret := range []string{"sk-abcdefghijklmnopqrstuvwxyz123456", "do-not-store-me"} {
		if strings.Contains(string(requestBody), secret) {
			t.Fatalf("model request leaked fixture secret %q", secret)
		}
	}
	if !strings.Contains(string(requestBody), "test-model") {
		t.Fatalf("model request missing configured model: %s", requestBody)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if report.Metrics.ModelCallCount != 1 || report.Metrics.ExceptionCounts["model_error"] != 0 {
		t.Fatalf("unexpected model metrics: %+v", report.Metrics)
	}
	if !hasRule(report, "LLM-SECRET") {
		t.Fatal("parsed model finding is missing")
	}
	if !strings.Contains(report.Summary, "model endpoint review succeeded") {
		t.Fatalf("model summary is missing: %q", report.Summary)
	}
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
}

// TestCleanFixtureReportsNoFindings verifies clean diffs stay silent.
func TestCleanFixtureReportsNoFindings(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		fixture:     "clean",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("clean fixture run: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if len(report.Findings) != 0 {
		t.Fatalf("clean fixture should have no findings: %+v", report.Findings)
	}
	if len(report.NeedsHumanReview) != 0 {
		t.Fatalf("clean fixture should need no human review: %+v",
			report.NeedsHumanReview)
	}
	if !strings.Contains(report.Summary, "No high-confidence findings") {
		t.Fatalf("summary should note a clean diff: %q", report.Summary)
	}
	if len(report.Metrics.ExceptionCounts) != 0 {
		t.Fatalf("clean fixture should record no exceptions: %+v",
			report.Metrics.ExceptionCounts)
	}
}

// TestSandboxFailureFixtureDegradesGracefully verifies sandbox errors do not abort reviews.
func TestSandboxFailureFixtureDegradesGracefully(t *testing.T) {
	repo := t.TempDir()
	// Mirror the fixture: a repository whose checks cannot compile.
	if err := os.WriteFile(filepath.Join(repo, "go.mod"),
		[]byte("module broken\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "broken.go"),
		[]byte("package broken\n\nfunc Broken() {\n\tif true {\n\t\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := config{
		fixture:     "sandbox_failure",
		repoPath:    repo,
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "local-dev",
		timeout:     2 * time.Minute,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("sandbox failure must not crash the review: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if report.Task.Status != review.StatusCompleted {
		t.Fatalf("task should complete despite sandbox failures: %+v",
			report.Task)
	}
	failed := 0
	for _, run := range report.SandboxRuns {
		if run.Status == "failed" {
			failed++
		}
	}
	if failed == 0 {
		t.Fatalf("expected failed sandbox runs: %+v", report.SandboxRuns)
	}
	counted := 0
	for _, count := range report.Metrics.ExceptionCounts {
		counted += count
	}
	if counted != failed {
		t.Fatalf("failed runs not counted as exceptions: failed=%d counts=%+v",
			failed, report.Metrics.ExceptionCounts)
	}
}

// TestRunRejectsUnknownMode verifies invalid --mode values fail fast.
func TestRunRejectsUnknownMode(t *testing.T) {
	err := run(context.Background(), config{mode: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unsupported --mode") {
		t.Fatalf("expected unsupported mode error, got %v", err)
	}
}

func TestValidateConfigRejectsConflictingInputsAndSandbox(t *testing.T) {
	base := config{mode: "rule-only", sandboxKind: "mock"}
	conflict := base
	conflict.fixture = "clean"
	conflict.diffFile = "change.diff"
	if err := validateConfig(conflict); err == nil {
		t.Fatal("conflicting inputs were accepted")
	}
	unknown := base
	unknown.fixture = "clean"
	unknown.sandboxKind = "unknown"
	if err := validateConfig(unknown); err == nil {
		t.Fatal("unknown sandbox was accepted")
	}
	filesWithRoot := base
	filesWithRoot.files = "a.go"
	filesWithRoot.repoPath = "."
	if err := validateConfig(filesWithRoot); err != nil {
		t.Fatalf("--files with --repo-path should be valid: %v", err)
	}
}

func TestValidateConfigRejectsManagedExecutionOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific managed sandbox preflight")
	}
	cfg := config{fixture: "clean", mode: "rule-only", sandboxKind: "managed"}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "unavailable on Windows") {
		t.Fatalf("managed execution was not rejected: %v", err)
	}
	cfg.dryRun = true
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("managed dry-run should remain portable: %v", err)
	}
}

func TestValidateConfigTaskQueryIgnoresRuntimeSettings(t *testing.T) {
	cfg := config{taskID: "task-1", mode: "invalid", sandboxKind: "invalid"}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("task query should not validate unused execution settings: %v", err)
	}
}

func TestLoadInputRejectsOversizedDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.diff")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxInputDiffBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = loadInput(context.Background(), config{diffFile: path})
	if err == nil || !strings.Contains(err.Error(), "input diff exceeds") {
		t.Fatalf("expected bounded input error, got %v", err)
	}
}

func TestRunRejectsTooManyChangedFiles(t *testing.T) {
	var diff strings.Builder
	for i := 0; i <= maxInputFiles; i++ {
		fmt.Fprintf(&diff, "diff --git a/f%d.go b/f%d.go\n--- /dev/null\n+++ b/f%d.go\n@@ -0,0 +1 @@\n+package p\n", i, i, i)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "many.diff")
	if err := os.WriteFile(path, []byte(diff.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), config{
		diffFile: path, outDir: filepath.Join(dir, "out"),
		dbPath: filepath.Join(dir, "review.db"), mode: "rule-only",
		sandboxKind: "mock",
	})
	if err == nil || !strings.Contains(err.Error(), "maximum is") {
		t.Fatalf("expected file-count limit error, got %v", err)
	}
}

// TestExpectedOutputsStayInSync re-runs every fixture and compares the
// result against the curated files under testdata/expected, so the samples
// never drift from the real reports. Regenerate them with
// testdata/gen_expected.go after intentional rule changes.
func TestExpectedOutputsStayInSync(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "expected", "*_review_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(fixtures) {
		t.Fatalf("expected outputs=%d fixtures=%d; every fixture needs a curated sample",
			len(paths), len(fixtures))
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), "_review_report.json")
		dir := t.TempDir()
		cfg := config{
			fixture:     name,
			outDir:      filepath.Join(dir, "out"),
			dbPath:      filepath.Join(dir, "review.db"),
			mode:        "rule-only",
			sandboxKind: "mock",
			dryRun:      true,
			timeout:     time.Second,
		}
		if err := run(context.Background(), cfg); err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
		actual := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var want struct {
			Summary          string           `json:"summary"`
			Findings         []review.Finding `json:"findings"`
			NeedsHumanReview []review.Finding `json:"needs_human_review"`
		}
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatalf("fixture %s: bad expected json: %v", name, err)
		}
		if actual.Summary != want.Summary {
			t.Fatalf("fixture %s summary drifted:\n got %q\nwant %q",
				name, actual.Summary, want.Summary)
		}
		assertSameFindings(t, name, "findings", actual.Findings, want.Findings)
		assertSameFindings(t, name, "needs_human_review",
			actual.NeedsHumanReview, want.NeedsHumanReview)
	}
}

func TestCommittedSampleOutputIsComplete(t *testing.T) {
	path := filepath.Join("sample_output", "review_report.json")
	report := readReport(t, path)
	if report.Task.ID == "" || len(report.Files) == 0 || len(report.Findings) == 0 ||
		len(report.SandboxRuns) == 0 || len(report.PermissionDecisions) == 0 ||
		len(report.FilterDecisions) == 0 || len(report.Artifacts) == 0 {
		t.Fatalf("sample report is incomplete: %+v", report)
	}
	if report.Metrics.FindingCount == 0 || report.Summary == "" {
		t.Fatalf("sample report lacks metrics or conclusion: %+v", report)
	}
	for _, name := range []string{"review_report.json", "review_report.md", "artifact_manifest.json"} {
		if _, err := os.Stat(filepath.Join("sample_output", name)); err != nil {
			t.Fatalf("sample artifact %s: %v", name, err)
		}
	}
	assertNoFixtureSecrets(t, path)
	assertNoFixtureSecrets(t, filepath.Join("sample_output", "review_report.md"))
}

// assertSameFindings compares actual and curated findings for one bucket.
func assertSameFindings(t *testing.T, fixture, bucket string, actual, want []review.Finding) {
	t.Helper()
	got := findingKeys(actual)
	expected := findingKeys(want)
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("fixture %s %s drifted:\n got %v\nwant %v",
			fixture, bucket, got, expected)
	}
}

// findingKeys reduces findings to comparable identity strings.
func findingKeys(fs []review.Finding) []string {
	keys := make([]string, 0, len(fs))
	for _, f := range fs {
		keys = append(keys, fmt.Sprintf("%s|%s|%d|%s", f.RuleID, f.File, f.Line, f.Severity))
	}
	sort.Strings(keys)
	return keys
}

// TestFilterDecisionsReportedAndPersisted verifies filter decisions reach report and DB.
func TestFilterDecisionsReportedAndPersisted(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		fixture:     "duplicate_findings",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if len(report.FilterDecisions) == 0 {
		t.Fatal("report should carry filter decisions")
	}
	if len(report.Metrics.FilterDecisionCounts) == 0 {
		t.Fatalf("metrics missing filter decision counts: %+v", report.Metrics)
	}
	total := 0
	for _, n := range report.Metrics.FilterDecisionCounts {
		total += n
	}
	if total != len(report.FilterDecisions) {
		t.Fatalf("counts=%d decisions=%d", total, len(report.FilterDecisions))
	}
	db, err := store.Open(context.Background(), cfg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot, err := db.GetTask(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.FilterDecisions) != len(report.FilterDecisions) {
		t.Fatalf("db decisions=%d report decisions=%d",
			len(snapshot.FilterDecisions), len(report.FilterDecisions))
	}
	mdData, err := os.ReadFile(filepath.Join(cfg.outDir, "review_report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mdData), "## Filter Decisions") {
		t.Fatal("markdown report missing Filter Decisions section")
	}
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.md"))
}

// TestTelemetrySpanRecordsReviewMetrics verifies OTLP spans carry review metrics.
func TestTelemetrySpanRecordsReviewMetrics(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := atrace.Tracer
	atrace.Tracer = tp.Tracer("code-review-test")
	t.Cleanup(func() {
		atrace.Tracer = oldTracer
		_ = tp.Shutdown(context.Background())
	})
	dir := t.TempDir()
	cfg := config{
		fixture:     "security_secret",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	var span sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "code_review.run" {
			span = s
			break
		}
	}
	if span == nil {
		t.Fatal("code_review.run span was not recorded")
	}
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	if got := attrs["code_review.task_id"].AsString(); got != report.Task.ID {
		t.Fatalf("span task_id=%q, want %q", got, report.Task.ID)
	}
	if got := attrs["code_review.finding_count"].AsInt64(); got != int64(len(report.Findings)) {
		t.Fatalf("span finding_count=%d, want %d", got, len(report.Findings))
	}
	if got := attrs["code_review.filter_decision_count"].AsInt64(); got != int64(len(report.FilterDecisions)) {
		t.Fatalf("span filter_decision_count=%d, want %d",
			got, len(report.FilterDecisions))
	}
	if _, ok := attrs["code_review.mode"]; !ok {
		t.Fatal("span missing code_review.mode attribute")
	}
}

func TestBuildMetricsCountsOnlyExecutedToolCalls(t *testing.T) {
	report := review.ReviewReport{
		SandboxRuns: []review.SandboxRun{
			{Status: "completed", DurationMS: 2},
			{Status: "failed", DurationMS: 3, FailureKind: review.FailureKindCommandExit},
			{Status: "blocked"},
			{Status: "skipped"},
		},
		PermissionDecisions: []review.PermissionDecision{
			{Decision: "allow"}, {Decision: "deny"}, {Decision: "needs_human_review"},
		},
	}
	metrics := buildMetrics(time.Now(), report)
	if metrics.ToolCallCount != 2 || metrics.BlockedCommandCount != 1 ||
		metrics.SkippedCommandCount != 1 {
		t.Fatalf("command counts: %+v", metrics)
	}
	if metrics.PermissionDenyCount != 1 || metrics.PermissionInterceptCount != 2 {
		t.Fatalf("permission counts: %+v", metrics)
	}
	if metrics.ExceptionCounts[review.FailureKindCommandExit] != 1 {
		t.Fatalf("exception counts: %+v", metrics.ExceptionCounts)
	}
}

// readReport loads a written review report from disk.
func readReport(t *testing.T, path string) review.ReviewReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report review.ReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// hasRule reports whether the report contains a finding with ruleID.
func hasRule(report review.ReviewReport, ruleID string) bool {
	for _, f := range report.Findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	for _, f := range report.NeedsHumanReview {
		if f.RuleID == ruleID {
			return true
		}
	}
	for _, f := range report.Warnings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

// assertNoFixtureSecrets fails if any fixture secret leaks into the file.
func assertNoFixtureSecrets(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	secrets := []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"do-not-store-me",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"abcdefghijklmnopqrstuvwxyz1234567890",
	}
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked %q", path, secret)
		}
	}
}
