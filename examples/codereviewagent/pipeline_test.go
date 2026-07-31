//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiffTracksAddedLines(t *testing.T) {
	data := []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+func added() {}\n")
	lines, err := parseUnifiedDiff(data)
	require.NoError(t, err)
	require.Equal(t, []changedLine{{File: "a.go", Line: 2, Content: "func added() {}"}}, lines)
}

func TestDedupeFindingsUsesFileLineAndRule(t *testing.T) {
	values := []finding{
		{File: "a.go", StartLine: 3, RuleID: "SEC002", Confidence: 0.8},
		{File: "a.go", StartLine: 3, RuleID: "SEC002", Confidence: 0.9},
	}
	result := dedupeFindings(values)
	require.Len(t, result, 1)
	require.Equal(t, 0.9, result[0].Confidence)
}

func TestRedactRemovesSecrets(t *testing.T) {
	result, changed := redact(`api_key="sk-abcdefghijklmnopqrstuvwxyz" token=ghp_abcdefghijklmnopqrstuvwxyz AKIAABCDEFGHIJKLMNOP`)
	require.True(t, changed)
	require.NotContains(t, result, "abcdefghijklmnopqrstuvwxyz")
	require.NotContains(t, result, "AKIAABCDEFGHIJKLMNOP")
	require.Contains(t, result, "[REDACTED]")
}

func TestSecretFixtureNeverPersistsSecretMaterial(t *testing.T) {
	output := t.TempDir()
	_, err := runReview(context.Background(), pipelineConfig{
		DiffFile: "fixtures/secret-redaction.diff", SkillsRoot: "skills", OutputDir: output,
		Database: filepath.Join(output, "review.db"), Mode: "test", Runner: drySandbox{},
	})
	require.NoError(t, err)
	for _, name := range []string{"review_report.json", "review_report.md", "review.db"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		require.NoError(t, err)
		require.NotContains(t, string(data), "sk-abcdefghijklmnopqrstuvwxyz012345")
	}
}

func TestReviewStoreRoundTrip(t *testing.T) {
	store, err := openReviewStore(filepath.Join(t.TempDir(), "review.db"))
	require.NoError(t, err)
	defer store.Close()
	report := &reviewReport{
		TaskID: "task-1", Status: "needs_attention", InputSource: "fixture.diff",
		DiffSHA256: "abc", CreatedAt: testTime(),
		Findings:   []finding{{File: "a.go", StartLine: 1, EndLine: 1, Severity: "P1", Category: "security", Confidence: 1, Source: "test", RuleID: "SEC", Status: "finding"}},
		Permission: permissionRecord{Action: "allow", Command: []string{"go", "test", "."}},
		Sandbox:    sandboxRun{Status: "dry_run"}, Metrics: reviewMetrics{FindingCount: 1}, Summary: "summary",
	}
	require.NoError(t, store.Save(context.Background(), report, []artifact{{Kind: "json", Path: "report.json", SHA256: "abc", SizeBytes: 1}}))
	stored, err := store.GetTask(context.Background(), report.TaskID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.FindingCount)
	require.Equal(t, 1, stored.SandboxCount)
	require.Equal(t, 1, stored.DecisionCount)
	require.Equal(t, 1, stored.ReportCount)
}

func TestSandboxFailureDoesNotAbortReview(t *testing.T) {
	output := t.TempDir()
	report, err := runReview(context.Background(), pipelineConfig{
		DiffFile: "fixtures/sandbox-failure.diff", SkillsRoot: "skills", OutputDir: output,
		Database: filepath.Join(output, "review.db"), Mode: "test", Runner: drySandbox{fail: true},
	})
	require.NoError(t, err)
	require.Equal(t, "failed", report.Sandbox.Status)
	require.Contains(t, ruleIDs(report.Findings), "SAN001")
}

func TestFullReviewWritesReportsAndDatabase(t *testing.T) {
	output := t.TempDir()
	report, err := runReview(context.Background(), pipelineConfig{
		DiffFile: "fixtures/command-injection.diff", SkillsRoot: "skills", OutputDir: output,
		Database: filepath.Join(output, "review.db"), Mode: "test", Runner: drySandbox{},
	})
	require.NoError(t, err)
	require.Equal(t, "needs_attention", report.Status)
	require.Contains(t, ruleIDs(report.Findings), "SEC002")
	_, err = os.Stat(filepath.Join(output, "review_report.json"))
	require.NoError(t, err)
	markdown, err := os.ReadFile(filepath.Join(output, "review_report.md"))
	require.NoError(t, err)
	require.Contains(t, string(markdown), "Governed Code Review Report")
	store, err := openReviewStore(filepath.Join(output, "review.db"))
	require.NoError(t, err)
	defer store.Close()
	stored, err := store.GetTask(context.Background(), report.TaskID)
	require.NoError(t, err)
	require.Equal(t, len(report.Findings), stored.FindingCount)
}

func TestPublicFixturesRunDeterministically(t *testing.T) {
	tests := []struct {
		name        string
		ruleID      string
		expectEmpty bool
	}{
		{name: "clean.diff", expectEmpty: true},
		{name: "command-injection.diff", ruleID: "SEC002"},
		{name: "goroutine-context.diff", ruleID: "CON001"},
		{name: "resource-leak.diff", ruleID: "RES001"},
		{name: "database-lifecycle.diff", ruleID: "DB001"},
		{name: "missing-tests.diff", ruleID: "TST001"},
		{name: "duplicate-findings.diff", ruleID: "SEC002"},
		{name: "sandbox-failure.diff", ruleID: "TST001"},
		{name: "secret-redaction.diff", ruleID: "SEC001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := t.TempDir()
			report, err := runReview(context.Background(), pipelineConfig{
				DiffFile: filepath.Join("fixtures", test.name), SkillsRoot: "skills", OutputDir: output,
				Database: filepath.Join(output, "review.db"), Mode: "fixture", Runner: drySandbox{},
			})
			require.NoError(t, err)
			if test.expectEmpty {
				require.Empty(t, report.Findings)
			}
			if test.ruleID != "" {
				require.Contains(t, ruleIDs(report.Findings), test.ruleID)
			}
		})
	}
}

func ruleIDs(findings []finding) []string {
	result := make([]string, 0, len(findings))
	for _, finding := range findings {
		result = append(result, finding.RuleID)
	}
	return result
}

func testTime() time.Time {
	return time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
}
