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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicFixturesGenerateReports(t *testing.T) {
	tests := []struct {
		fixture      string
		expectedRule string
		failSandbox  bool
	}{
		{fixture: "clean.diff"},
		{fixture: "security.diff", expectedRule: "SEC002"},
		{fixture: "goroutine_context.diff", expectedRule: "CON001"},
		{fixture: "resource_leak.diff", expectedRule: "RES001"},
		{fixture: "database_lifecycle.diff", expectedRule: "DB001"},
		{fixture: "missing_tests.diff", expectedRule: "TST001"},
		{fixture: "duplicate_finding.diff", expectedRule: "SEC002"},
		{fixture: "sensitive_info.diff", expectedRule: "SEC001"},
		{fixture: "sandbox_failure.diff", failSandbox: true},
		{fixture: "error_handling.diff", expectedRule: "ERR001"},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			diff, err := os.ReadFile(
				filepath.Join("testdata", "fixtures", test.fixture),
			)
			require.NoError(t, err)
			outputDir := t.TempDir()
			store, err := NewSQLiteStore(filepath.Join(outputDir, "reviews.db"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			sandbox := &FakeSandbox{}
			if test.failSandbox {
				sandbox.FailCommand = "bash"
			}
			reviewer, err := NewReviewer(store, sandbox, "skills")
			require.NoError(t, err)
			report, err := reviewer.Review(
				context.Background(),
				ReviewRequest{
					Diff: diff, InputKind: "fixture", Runtime: "fake",
					DryRun: true, OutputDir: outputDir,
				},
			)
			require.NoError(t, err)
			require.FileExists(
				t, filepath.Join(outputDir, "review_report.json"),
			)
			require.FileExists(
				t, filepath.Join(outputDir, "review_report.md"),
			)
			if test.expectedRule != "" {
				all := append(
					append([]Finding(nil), report.Findings...),
					report.Warnings...,
				)
				requireFindingRule(t, all, test.expectedRule)
			}
			if test.fixture == "clean.diff" {
				require.Empty(t, report.Findings)
				require.Empty(t, report.Warnings)
			}
			if test.failSandbox {
				require.Equal(t, "completed_with_errors", report.Status)
			}
		})
	}
}

func TestDuplicateFixtureProducesOneSecurityFindingAtLine(t *testing.T) {
	diff, err := os.ReadFile(
		filepath.Join("testdata", "fixtures", "duplicate_finding.diff"),
	)
	require.NoError(t, err)
	parsed, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	findings, _ := AnalyzeDiff(parsed)
	count := 0
	for _, finding := range findings {
		if finding.File == "query.go" && finding.Line == 3 &&
			finding.Category == "security" {
			count++
		}
	}
	require.Equal(t, 1, count)
}
