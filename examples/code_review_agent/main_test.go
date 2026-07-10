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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// allRuleIDs is the complete set of rule IDs implemented by the rules engine.
// It is used by the clean-fixture assertion to verify that no rules fire on a
// benign diff.
var allRuleIDs = []string{
	"SI-001", "SC-001", "GL-001", "GL-002",
	"RL-001", "EH-001", "TM-001", "DB-001",
}

// fixtureCase describes a single integration test case.
type fixtureCase struct {
	name             string // sub-test name
	fixtureFile      string // .diff file under testdata/fixtures/
	expectFinding    string // rule ID expected in the report; "" skips the check
	noFindings       bool   // if true, assert no rule IDs appear in the report
	expectConclusion string // expected conclusion string in the JSON report
	plaintextSecret  string // secret value that must NOT appear in the report
}

// TestIntegration_Fixtures runs the full review pipeline against each .diff
// fixture and verifies that reports are generated, expected findings appear,
// conclusions are correct, and no plaintext secrets leak into the output.
//
// The pipeline is invoked directly via runPipeline (rather than exec.Command)
// to avoid recompilation overhead per fixture. Each fixture is copied into a
// fresh temp directory so the fixture-dir loader sees exactly one diff file.
//
// Sandbox behaviour note: with --executor local --unsafe-local the sandbox
// runs `go vet` and `staticcheck` inside an empty workspace (no go.mod, no
// staged files). These commands fail, producing StatusFailed sandbox runs.
// Per computeConclusion, a failed sandbox run yields conclusion
// "needs_human_review" unless a critical-severity finding forces "fail".
func TestIntegration_Fixtures(t *testing.T) {
	fixtures := []fixtureCase{
		{"clean", "clean.diff", "", true, "needs_human_review", ""},
		{"security", "security.diff", "SI-001", false, "fail", "sk-abc123def456ghi789jkl012mno345"},
		{"goroutine_leak", "goroutine_leak.diff", "GL-001", false, "needs_human_review", ""},
		{"resource_leak", "resource_leak.diff", "RL-001", false, "needs_human_review", ""},
		{"missing_tests", "missing_tests.diff", "TM-001", false, "needs_human_review", ""},
		{"sensitive_info", "sensitive_info.diff", "SC-001", false, "fail", "super-secret-value-12345"},
		{"db_lifecycle", "db_lifecycle.diff", "DB-001", false, "needs_human_review", ""},
		{"duplicate_finding", "duplicate_finding.diff", "SI-001", false, "fail", "sk-duplicate001test002value003"},
		{"sandbox_failure", "sandbox_failure.diff", "", false, "needs_human_review", ""},
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
// SQLite database files were created by the pipeline.
func verifyReportArtifacts(t *testing.T, outDir, dbPath string) {
	t.Helper()
	jsonPath := filepath.Join(outDir, "review_report.json")
	mdPath := filepath.Join(outDir, "review_report.md")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("json report missing: %v", err)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Errorf("md report missing: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db missing: %v", err)
	}
}

// readReportJSON reads and returns the JSON report as a string.
func readReportJSON(t *testing.T, outDir string) string {
	t.Helper()
	jsonPath := filepath.Join(outDir, "review_report.json")
	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	return string(jsonBytes)
}

// checkConclusion verifies the expected conclusion string appears in the report.
func checkConclusion(t *testing.T, tt fixtureCase, jsonStr string) {
	t.Helper()
	if !strings.Contains(jsonStr, tt.expectConclusion) {
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
