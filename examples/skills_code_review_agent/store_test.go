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
	"time"

	"github.com/stretchr/testify/require"
)

func TestSQLiteStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	now := time.Now().UTC().Truncate(time.Microsecond)
	report := ReviewReport{
		TaskID: "review-1", Status: "completed",
		Conclusion: "changes_requested", Mode: "rule-only",
		Runtime: "fake", Skill: "code-review",
		StartedAt: now, CompletedAt: now.Add(time.Second),
		Input: InputSummary{
			Kind: "fixture", SHA256: "abc", Bytes: 10,
			ChangedFiles: []string{"main.go"},
			GoPackages:   []string{"main"},
		},
		Findings: []Finding{{
			Severity: severityHigh, Category: "resource_lifecycle",
			File: "main.go", Line: 7, Title: "resource leak",
			Evidence: "f := os.Open(...)", Recommendation: "close f",
			Confidence: 0.95, Source: sourceRule, RuleID: "RES001",
		}},
		Decisions: []PermissionDecision{{
			Tool: "workspace_exec", Command: "go test ./...",
			Action: "allow", Risk: "low", CreatedAt: now,
		}},
		SandboxRuns: []SandboxRun{{
			Command: "go test ./...", Status: "passed",
			ExitCode: 0, DurationMS: 10,
		}},
		Metrics: Metrics{
			ToolCalls: 1, FindingCount: 1,
			Severity: map[string]int{severityHigh: 1},
			Errors:   map[string]int{},
		},
	}
	require.NoError(t, store.SaveReview(ctx, report, []byte(`{"task_id":"review-1"}`), []byte("# Report")))

	got, err := store.GetReview(ctx, "review-1")
	require.NoError(t, err)
	require.Equal(t, report.TaskID, got.TaskID)
	require.Equal(t, report.Findings, got.Findings)
	require.Equal(t, report.Decisions[0].Action, got.Decisions[0].Action)
	require.Equal(t, report.SandboxRuns[0].Status, got.SandboxRuns[0].Status)
	require.Equal(t, report.Metrics.Severity, got.Metrics.Severity)

	jsonReport, markdownReport, err := store.GetReport(ctx, "review-1")
	require.NoError(t, err)
	require.JSONEq(t, `{"task_id":"review-1"}`, string(jsonReport))
	require.Equal(t, "# Report", string(markdownReport))

	raw, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-live-secret")
}
