//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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

func TestCodeReviewer_8Scenarios(t *testing.T) {
	reviewer, err := NewCodeReviewer(CodeReviewerOptions{
		SkillsDir:  "./skills",
		DBPath:     ":memory:",
		UseSandbox: true,
	})
	require.NoError(t, err)
	defer reviewer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	scenarios := []struct {
		name          string
		fixture       string
		expectFinding bool
		minFindings   int
	}{
		{"01 Clean Diff", "testdata/01_clean.diff", false, 0},
		{"02 Security Secret Leak", "testdata/02_security_secret.diff", true, 1},
		{"03 Goroutine Leak", "testdata/03_goroutine_leak.diff", true, 1},
		{"04 Unclosed Resource", "testdata/04_unclosed_resource.diff", true, 1},
		{"05 DB Transaction Lifecycle", "testdata/05_db_transaction.diff", true, 1},
		{"06 Missing Unit Tests", "testdata/06_missing_tests.diff", true, 1},
		{"07 Duplicate Findings", "testdata/07_duplicate_findings.diff", true, 1},
		{"08 Sandbox Failure & Governance", "testdata/08_sandbox_failure.diff", false, 0},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			diff, err := GenerateDiffFromFixture(sc.fixture)
			require.NoError(t, err)

			input := ReviewTaskInput{
				TaskID:   "task-" + sc.name,
				RepoPath: ".",
				DiffText: diff,
			}

			result, err := reviewer.ExecuteReview(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "completed", result.Status)

			if sc.expectFinding {
				require.GreaterOrEqual(t, len(result.Findings), sc.minFindings)
			}

			// Verify DB persistence
			dbFindings, err := reviewer.storage.GetTaskFindings(input.TaskID)
			require.NoError(t, err)
			require.Equal(t, len(result.Findings), len(dbFindings))
		})
	}
}

func TestRedactSecret(t *testing.T) {
	input := `apiKey := "sk-proj-1234567890secretkeyvalue"`
	redacted := RedactSecret(input)
	require.NotContains(t, redacted, "1234567890secretkeyvalue")
	require.Contains(t, redacted, "sk****ue")
}

func TestDeduplicateFindings(t *testing.T) {
	findings := []Finding{
		{File: "main.go", Line: 10, Category: "Resource Leak", Title: "Unclosed body"},
		{File: "main.go", Line: 10, Category: "Resource Leak", Title: "Unclosed body duplicate"},
		{File: "main.go", Line: 12, Category: "Resource Leak", Title: "Another line"},
	}

	deduped := DeduplicateFindings(findings)
	require.Len(t, deduped, 2)
}

func TestPermissionPolicy(t *testing.T) {
	reviewer, err := NewCodeReviewer(CodeReviewerOptions{DBPath: ":memory:"})
	require.NoError(t, err)
	defer reviewer.Close()

	dec, _ := reviewer.CheckPermission("rm -rf /")
	require.Equal(t, "deny", dec)

	dec, _ = reviewer.CheckPermission("go test ./...")
	require.Equal(t, "allow", dec)

	dec, _ = reviewer.CheckPermission("sudo systemctl restart")
	require.Equal(t, "ask", dec)
}

func TestReportGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	jsonPath := filepath.Join(tmpDir, "report.json")
	mdPath := filepath.Join(tmpDir, "report.md")

	res := &ReviewResult{
		TaskID:   "task-test-report",
		Status:   "completed",
		RepoPath: ".",
		Findings: []Finding{
			{
				ID:             "f-1",
				TaskID:         "task-test-report",
				Severity:       "high",
				Category:       "Security Risk",
				File:           "auth.go",
				Line:           25,
				Title:          "Hardcoded Token",
				Evidence:       "token := sk****",
				Recommendation: "Use env var",
				Confidence:     0.99,
				Source:         "static_rule",
				RuleID:         "GOP-004",
			},
		},
		Metrics: ReviewMetrics{
			SeverityCounts: map[string]int{"high": 1},
		},
		DurationMs: 150,
	}

	err := SaveReportJSON(res, jsonPath)
	require.NoError(t, err)
	_, err = os.Stat(jsonPath)
	require.NoError(t, err)

	err = SaveReportMarkdown(res, mdPath)
	require.NoError(t, err)
	_, err = os.Stat(mdPath)
	require.NoError(t, err)
}
