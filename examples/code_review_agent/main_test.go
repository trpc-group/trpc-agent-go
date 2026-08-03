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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if report.Metrics.ExceptionCounts["failed"] != failed {
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

// TestSkillsRootIgnoresWorkingDirectory ensures a reviewed repository
// cannot shadow the bundled skills by planting its own skills tree in
// the working directory; only --skills-root may point elsewhere.
func TestSkillsRootIgnoresWorkingDirectory(t *testing.T) {
	forged := t.TempDir()
	forgedSkill := filepath.Join(forged, "skills", "code-review")
	if err := os.MkdirAll(forgedSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(forged); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	got := resolveSkillsRoot("")
	if abs, err := filepath.Abs(got); err == nil {
		got = abs
	}
	if strings.HasPrefix(got, forged) {
		t.Fatalf("default skills root %q trusts the working directory", got)
	}
	if want := filepath.Join(exampleDir(), "skills"); got != want {
		t.Fatalf("skills root = %q, want bundled %q", got, want)
	}
	// An explicit flag still wins so users can opt into custom skills.
	if got := resolveSkillsRoot(forgedSkill); got != forgedSkill {
		t.Fatalf("explicit skills root = %q, want %q", got, forgedSkill)
	}
}

// failingStore wraps a real store and fails on SaveFindings so tests
// can drive the persistence failure path of runOne.
type failingStore struct{ store.Store }

// SaveFindings always fails to simulate a mid-persistence outage.
func (f *failingStore) SaveFindings(ctx context.Context, taskID string, findings []review.Finding) error {
	return errors.New("injected findings insert failure")
}

// TestPersistenceFailureUnpublishesReport verifies a store failure both
// fails the task and removes the already-written report artifacts, so
// published output never claims success for a failed audit trail.
func TestPersistenceFailureUnpublishesReport(t *testing.T) {
	oldOpen := openStoreFn
	openStoreFn = func(ctx context.Context, path string) (store.Store, error) {
		real, err := store.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		return &failingStore{Store: real}, nil
	}
	t.Cleanup(func() { openStoreFn = oldOpen })

	dir := t.TempDir()
	cfg := config{
		fixture:     "security_secret",
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     5 * time.Second,
	}
	err := runOne(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "injected findings insert failure") {
		t.Fatalf("expected injected store error, got %v", err)
	}
	for _, name := range []string{"review_report.json", "review_report.md"} {
		if _, statErr := os.Stat(filepath.Join(cfg.outDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("artifact %s still published after store failure", name)
		}
	}
	db, err := sql.Open("sqlite3", cfg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	if err := db.QueryRow("SELECT status FROM review_tasks").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
}

// TestPathBearingFieldsRedactedEverywhere reviews a diff whose file name
// carries a credential and verifies neither the JSON artifact nor the
// raw database rows retain the raw values.
func TestPathBearingFieldsRedactedEverywhere(t *testing.T) {
	const rawKey = "AKIA1234567890ABCDEF"
	const rawPass = "correct horse battery staple"
	diff := "diff --git a/cfg/" + rawKey + ".go b/cfg/" + rawKey + ".go\n" +
		"--- a/cfg/" + rawKey + ".go\n" +
		"+++ b/cfg/" + rawKey + ".go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package cfg\n" +
		"+var password = \"" + rawPass + "\"\n"
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "leak.diff")
	if err := os.WriteFile(diffPath, []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	dbPath := filepath.Join(dir, "review.db")
	cfg := config{
		diffFile:    diffPath,
		outDir:      outDir,
		dbPath:      dbPath,
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     5 * time.Second,
	}
	if err := runOne(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review_report.json", "review_report.md"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{rawKey, rawPass} {
			if strings.Contains(string(data), leak) {
				t.Fatalf("%s leaks %q", name, leak)
			}
		}
	}
	// Inspect the raw rows, not the redacting read path.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		"SELECT file FROM review_findings",
		"SELECT file FROM filter_decisions",
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var file string
			if err := rows.Scan(&file); err != nil {
				t.Fatal(err)
			}
			count++
			if strings.Contains(file, rawKey) {
				t.Fatalf("%s row leaks the raw path: %q", query, file)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count == 0 {
			t.Fatalf("%s returned no rows to verify", query)
		}
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
