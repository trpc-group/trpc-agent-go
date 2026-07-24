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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/reporter"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/storage"
)

// TestParseCleanDiff verifies that a clean diff produces no findings.
func TestParseCleanDiff(t *testing.T) {
	data, err := os.ReadFile("testdata/clean.diff")
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := parser.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) == 0 {
		t.Fatal("expected at least one FileDiff")
	}
	findings := rules.Run(diffs)
	if len(findings) != 0 {
		t.Errorf("clean diff produced %d findings, want 0: %+v", len(findings), findings)
	}
}

// TestGoroutineLeakDetected verifies GL-001 fires on the goroutine_leak diff.
func TestGoroutineLeakDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/goroutine_leak.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "GL-001") {
		t.Errorf("expected GL-001 finding, got: %+v", findings)
	}
}

// TestResourceNotClosedDetected verifies RL-001 fires on the resource_not_closed diff.
func TestResourceNotClosedDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/resource_not_closed.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "RL-001") {
		t.Errorf("expected RL-001 finding, got: %+v", findings)
	}
}

// TestErrorNotHandledDetected verifies EH-001 fires on the error_not_handled diff.
func TestErrorNotHandledDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/error_not_handled.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "EH-001") {
		t.Errorf("expected EH-001 finding, got: %+v", findings)
	}
}

// TestSensitiveInfoDetected verifies SI-001 fires and redacts the value.
func TestSensitiveInfoDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/sensitive_info.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "SI-001") {
		t.Errorf("expected SI-001 finding, got: %+v", findings)
	}
	for _, f := range findings {
		if f.RuleID != "SI-001" {
			continue
		}
		// Evidence must not contain the literal secret value.
		if strings.Contains(f.Evidence, "hardcoded123") ||
			strings.Contains(f.Evidence, "sk-abc123secretvalue") {
			t.Errorf("sensitive value not redacted in evidence: %q", f.Evidence)
		}
		if !strings.Contains(f.Evidence, "[REDACTED]") {
			t.Errorf("expected [REDACTED] marker in evidence: %q", f.Evidence)
		}
	}
}

// TestDeduplication verifies that running rules twice does not double-report findings.
func TestDeduplication(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/goroutine_leak.diff")
	// Run twice and check counts are identical (dedup is within a single Run call).
	first := rules.Run(diffs)
	second := rules.Run(diffs)
	countFirst := countByRuleID(first, "GL-001")
	countSecond := countByRuleID(second, "GL-001")
	if countFirst != countSecond {
		t.Errorf("non-deterministic finding count: %d vs %d", countFirst, countSecond)
	}
	// Within a single run, the same (file, line, ruleID) must not appear twice.
	type key struct {
		file   string
		line   int
		ruleID string
	}
	seen := map[key]int{}
	for _, f := range first {
		k := key{f.File, f.Line, f.RuleID}
		seen[k]++
		if seen[k] > 1 {
			t.Errorf("duplicate finding: %+v", f)
		}
	}
}

// TestStorageRoundTrip verifies insert + query returns the same findings.
func TestStorageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	taskID := "test-task-001"
	if err := db.InsertTask(taskID, "abc123", ""); err != nil {
		t.Fatal(err)
	}

	rows := []storage.FindingRow{
		{
			TaskID:         taskID,
			Severity:       "high",
			Category:       "goroutine_leak",
			File:           "pkg/worker/worker.go",
			Line:           10,
			Title:          "Goroutine started without synchronisation",
			Evidence:       "go func() {",
			Recommendation: "Add WaitGroup or context cancellation.",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "GL-001",
		},
	}
	if err := db.InsertFindings(rows); err != nil {
		t.Fatal(err)
	}

	got, err := db.QueryTaskFindings(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(got))
	}
	if got[0].RuleID != "GL-001" || got[0].File != "pkg/worker/worker.go" {
		t.Errorf("unexpected finding: %+v", got[0])
	}
}

// TestSQLInjectionDetected verifies SQL-001 fires on string-concatenated queries.
func TestSQLInjectionDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/sql_injection.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "SQL-001") {
		t.Errorf("expected SQL-001 finding, got: %+v", findings)
	}
}

// TestHTTPBodyLeakDetected verifies RL-002 fires when response body is not closed.
func TestHTTPBodyLeakDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/http_body_leak.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "RL-002") {
		t.Errorf("expected RL-002 finding, got: %+v", findings)
	}
}

// TestSandboxFailDoesNotCrash verifies that a diff with the SANDBOX_SHOULD_FAIL
// marker does not panic or fatal when processed through rules.
func TestSandboxFailDoesNotCrash(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/sandbox_fail.diff")
	// Must not panic.
	findings := rules.Run(diffs)
	// sandbox_fail.diff intentionally has a goroutine leak and sensitive info.
	_ = findings
}

// TestReportPartitionsWarnings verifies that low-confidence findings go to Warnings.
func TestReportPartitionsWarnings(t *testing.T) {
	findings := []rules.Finding{
		{Severity: "high", RuleID: "GL-001", Confidence: "medium", File: "a.go", Line: 1},
		{Severity: "medium", RuleID: "EH-001", Confidence: "low", File: "a.go", Line: 5},
	}
	rpt := reporter.Build("task-1", "test.diff", "", findings, reporter.Metrics{})
	if len(rpt.Findings) != 1 {
		t.Errorf("expected 1 confirmed finding, got %d", len(rpt.Findings))
	}
	if len(rpt.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(rpt.Warnings))
	}
}

// TestDBConnectionLeakDetected verifies RL-001 fires on db_connection.diff.
func TestDBConnectionLeakDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/db_connection.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "RL-001") {
		t.Errorf("expected RL-001 for sql.Open without defer close, got: %+v", findings)
	}
}

// TestContextBackgroundMisuseDetected verifies CC-001 fires when context.Background() is used inside a function with ctx parameter.
func TestContextBackgroundMisuseDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/context_background_misuse.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "CC-001") {
		t.Errorf("expected CC-001 finding, got: %+v", findings)
	}
}

// TestMutexNoDeferDetected verifies MT-001 fires when Lock() has no paired defer Unlock().
func TestMutexNoDeferDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/mutex_no_defer.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "MT-001") {
		t.Errorf("expected MT-001 finding, got: %+v", findings)
	}
}

// TestDataRaceDetected verifies MT-002 fires when goroutine closure captures variable without sync.
func TestDataRaceDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/data_race.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "MT-002") {
		t.Errorf("expected MT-002 finding, got: %+v", findings)
	}
}

// TestStringConcatLoopDetected verifies PF-001 fires for += inside for loop.
func TestStringConcatLoopDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/string_concat_loop.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "PF-001") {
		t.Errorf("expected PF-001 finding, got: %+v", findings)
	}
}

// TestFmtSprintfConvDetected verifies PF-002 fires for fmt.Sprintf("%d", x).
func TestFmtSprintfConvDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/fmt_sprintf_conv.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "PF-002") {
		t.Errorf("expected PF-002 finding, got: %+v", findings)
	}
}

// TestBareReturnErrDetected verifies ER-001 fires for unwrapped error returns.
func TestBareReturnErrDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/bare_return_err.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "ER-001") {
		t.Errorf("expected ER-001 finding, got: %+v", findings)
	}
}

// TestDeferInLoopDetected verifies DP-001 fires for defer inside for loop.
func TestDeferInLoopDetected(t *testing.T) {
	diffs := mustParseDiff(t, "testdata/defer_in_loop.diff")
	findings := rules.Run(diffs)
	if !hasRuleID(findings, "DP-001") {
		t.Errorf("expected DP-001 finding, got: %+v", findings)
	}
}

// --- helpers ---

func mustParseDiff(t *testing.T, path string) []parser.FileDiff {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	diffs, err := parser.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return diffs
}

func hasRuleID(findings []rules.Finding, ruleID string) bool {
	for _, f := range findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

func countByRuleID(findings []rules.Finding, ruleID string) int {
	n := 0
	for _, f := range findings {
		if f.RuleID == ruleID {
			n++
		}
	}
	return n
}
