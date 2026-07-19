//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main contains integration tests that exercise the full review
// pipeline (input loading, rule engine, sandbox checks, report generation,
// persistence) against a curated set of .diff fixtures.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// allRuleIDs is the complete set of rule IDs implemented by the rules engine
// plus the AST rule engine, the fake LLM heuristics, and the sandbox
// diagnostic parser. It is used by the clean-fixture assertion to verify
// that no rules fire on a benign diff.
var allRuleIDs = []string{
	"SI-001", "SC-001", "SC-002", "SC-003",
	"GL-001", "GL-002", "GL-003",
	"RL-001", "EH-001", "TM-001",
	"DB-001", "DB-002",
	"AST-001", "AST-002", "AST-003", "AST-004",
	"LLM-001", "LLM-002", "LLM-003",
	"DIAG-001", "DIAG-002",
}

// fixtureCase describes a single integration test case.
type fixtureCase struct {
	name             string // sub-test name
	fixtureFile      string // .diff file under testdata/fixtures/
	expectFinding    string // rule ID expected in the report; "" skips the check
	noFindings       bool   // if true, assert no rule IDs appear in the report
	expectConclusion string // expected conclusion string in the JSON report
	plaintextSecret  string // secret value that must NOT appear in the report
	model            string // --model flag value; "" leaves the default (rule-based only)
}

// TestIntegration_Fixtures runs the full review pipeline against each .diff
// fixture and verifies that reports are generated, expected findings appear,
// conclusions are correct, and no plaintext secrets leak into the output.
//
// The pipeline is invoked directly via runPipeline (rather than exec.Command)
// to avoid recompilation overhead per fixture. Each fixture is copied into a
// fresh temp directory so the fixture-dir loader sees exactly one diff file.
//
// Sandbox behaviour note: these fixtures use --fixture-dir mode (no
// --repo-path), so runSandboxChecks skips the static checks entirely and
// records a single StatusSkipped run. The conclusion is therefore driven
// purely by rule findings: "fail" when a critical-severity finding is
// present, "pass" otherwise.
func TestIntegration_Fixtures(t *testing.T) {
	fixtures := []fixtureCase{
		{name: "clean", fixtureFile: "clean.diff", noFindings: true, expectConclusion: "pass"},
		{name: "security", fixtureFile: "security.diff", expectFinding: "SI-001", expectConclusion: "fail", plaintextSecret: "sk-abc123def456ghi789jkl012mno345"},
		{name: "goroutine_leak", fixtureFile: "goroutine_leak.diff", expectFinding: "GL-001", expectConclusion: "pass"},
		{name: "resource_leak", fixtureFile: "resource_leak.diff", expectFinding: "RL-001", expectConclusion: "pass"},
		{name: "missing_tests", fixtureFile: "missing_tests.diff", expectFinding: "TM-001", expectConclusion: "pass"},
		{name: "sensitive_info", fixtureFile: "sensitive_info.diff", expectFinding: "SC-001", expectConclusion: "fail", plaintextSecret: "super-secret-value-12345"},
		{name: "db_lifecycle", fixtureFile: "db_lifecycle.diff", expectFinding: "DB-001", expectConclusion: "pass"},
		{name: "duplicate_finding", fixtureFile: "duplicate_finding.diff", expectFinding: "SI-001", expectConclusion: "fail", plaintextSecret: "sk-duplicate001test002value003"},
		{name: "sandbox_failure", fixtureFile: "sandbox_failure.diff", expectConclusion: "pass"},
		// Phase-1 new rules (borrowed from competitor PRs #2190/#2243):
		{name: "missing_tx_rollback", fixtureFile: "missing_tx_rollback.diff", expectFinding: "DB-002", expectConclusion: "pass"},
		{name: "panic_in_goroutine", fixtureFile: "panic_in_goroutine.diff", expectFinding: "GL-003", expectConclusion: "pass"},
		{name: "cmd_injection", fixtureFile: "cmd_injection.diff", expectFinding: "SC-002", expectConclusion: "fail"},
		{name: "sensitive_info_in_log", fixtureFile: "sensitive_info_in_log.diff", expectFinding: "SC-003", expectConclusion: "pass"},
		// Phase-3 AST rules (borrowed from competitor PR #2243):
		{name: "ast_http_body_leak", fixtureFile: "ast_http_body_leak.diff", expectFinding: "AST-001", expectConclusion: "pass"},
		{name: "ast_sql_rows_leak", fixtureFile: "ast_sql_rows_leak.diff", expectFinding: "AST-002", expectConclusion: "pass"},
		{name: "ast_context_misuse", fixtureFile: "ast_context_misuse.diff", expectFinding: "AST-003", expectConclusion: "pass"},
		{name: "ast_goroutine_shared_mutation", fixtureFile: "ast_goroutine_shared_mutation.diff", expectFinding: "AST-004", expectConclusion: "pass"},
		// AST benign: a complete Go file that uses http.Get but properly
		// defers Body.Close — none of the AST rules should fire.
		{name: "ast_http_body_closed", fixtureFile: "ast_http_body_closed.diff", noFindings: true, expectConclusion: "pass"},
		// Phase-3.2 fake LLM (borrowed from competitor PR #2243):
		// --model=fake exercises the LLM integration path without API
		// keys. The fixture contains a hardcoded password (LLM-001), a
		// TODO comment (LLM-002), and a debug print (LLM-003). SI-001
		// also fires on the password; the test only asserts LLM-001
		// appears, proving the fake model's output reached the report.
		{name: "llm_patterns", fixtureFile: "llm_patterns.diff", expectFinding: "LLM-001", expectConclusion: "fail", plaintextSecret: "super-secret-value-12345", model: "fake"},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			fixtureDir := filepath.Join(tmpDir, "fixtures")
			if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
				t.Fatalf("mkdir fixtures: %v", err)
			}
			src := filepath.Join("testdata", "fixtures", tt.fixtureFile)
			data, err := os.ReadFile(src)
			if err != nil {
				t.Skipf("fixture %s not found: %v", tt.fixtureFile, err)
			}
			dst := filepath.Join(fixtureDir, tt.fixtureFile)
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			outDir := filepath.Join(tmpDir, "out")
			dbPath := filepath.Join(tmpDir, "review.db")

			opts := &pipelineOpts{
				cliFlags: cliFlags{
					fixtureDir:  fixtureDir,
					outDir:      outDir,
					dbPath:      dbPath,
					executor:    "local",
					unsafeLocal: true,
					dryRun:      true,
					model:       tt.model,
				},
				startTime: time.Now(),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			if err := runPipeline(ctx, opts); err != nil {
				t.Fatalf("runPipeline failed: %v", err)
			}

			verifyReportArtifacts(t, outDir, dbPath)
			jsonStr := readReportJSON(t, outDir)
			checkConclusion(t, tt, jsonStr)
			checkFinding(t, tt, jsonStr)
			checkNoFindings(t, tt, jsonStr)
			checkNoPlaintextSecret(t, tt, jsonStr)
		})
	}
}

// verifyReportArtifacts checks that the JSON report, Markdown report, and
// SQLite database files were created by the pipeline. The report filenames
// include the per-run task id, so we glob for the per-task pattern rather
// than expecting a fixed name.
func verifyReportArtifacts(t *testing.T, outDir, dbPath string) {
	t.Helper()
	jsonMatches, err := filepath.Glob(filepath.Join(outDir, "review_report_*.json"))
	if err != nil || len(jsonMatches) == 0 {
		t.Errorf("json report missing in %q (glob err: %v)", outDir, err)
	}
	mdMatches, err := filepath.Glob(filepath.Join(outDir, "review_report_*.md"))
	if err != nil || len(mdMatches) == 0 {
		t.Errorf("md report missing in %q (glob err: %v)", outDir, err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db missing: %v", err)
	}
}

// readReportJSON reads and returns the JSON report as a string. The report
// filename includes the per-run task id, so we glob for the per-task pattern.
func readReportJSON(t *testing.T, outDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(outDir, "review_report_*.json"))
	if err != nil {
		t.Fatalf("glob json report: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no json report found in %q", outDir)
	}
	jsonBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read json report %q: %v", matches[0], err)
	}
	return string(jsonBytes)
}

// checkConclusion verifies the expected conclusion string appears as the
// "Conclusion" JSON value in the report. Anchoring on the key avoids false
// matches from other fields like "status":"failed".
func checkConclusion(t *testing.T, tt fixtureCase, jsonStr string) {
	t.Helper()
	want := fmt.Sprintf(`"Conclusion": "%s"`, tt.expectConclusion)
	if !strings.Contains(jsonStr, want) {
		t.Errorf("conclusion %q not in report; snippet: %s",
			tt.expectConclusion, truncate(jsonStr, 500))
	}
}

// checkFinding verifies the expected rule ID appears in the report.
func checkFinding(t *testing.T, tt fixtureCase, jsonStr string) {
	t.Helper()
	if tt.expectFinding != "" && !strings.Contains(jsonStr, tt.expectFinding) {
		t.Errorf("expected finding %s not in report", tt.expectFinding)
	}
}

// checkNoFindings verifies that no rule IDs appear in the report (for the
// clean fixture).
func checkNoFindings(t *testing.T, tt fixtureCase, jsonStr string) {
	t.Helper()
	if !tt.noFindings {
		return
	}
	for _, rid := range allRuleIDs {
		if strings.Contains(jsonStr, rid) {
			t.Errorf("clean fixture should have no findings, but found %s", rid)
		}
	}
}

// checkNoPlaintextSecret verifies that the given secret value does not appear
// in the report (redaction check).
func checkNoPlaintextSecret(t *testing.T, tt fixtureCase, jsonStr string) {
	t.Helper()
	if tt.plaintextSecret != "" && strings.Contains(jsonStr, tt.plaintextSecret) {
		t.Errorf("plaintext secret %q found in report", tt.plaintextSecret)
	}
}

// truncate returns s truncated to at most n characters, with "..." appended
// if truncation occurred.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
