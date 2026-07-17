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
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreSaveAndLoadReport(t *testing.T) {
	ctx := context.Background()
	_, err := OpenStore(ctx, "")
	require.Error(t, err)

	dbPath := filepath.Join(t.TempDir(), "review.db")
	store, err := OpenStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	report := minimalReport("task-1")
	require.NoError(t, store.SaveReport(ctx, report, "report.json", "report.md"))
	got, err := store.LoadReport(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", got.Task.ID)
	latest, err := store.LoadLatestTaskIDByDiffHash(ctx, report.Task.DiffHash)
	require.NoError(t, err)
	require.Equal(t, "task-1", latest)

	task, err := store.LoadTask(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, taskStatusCompleted, task.Status)
	permissions, err := store.LoadPermissionDecisions(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, permissions, 1)
	filters, err := store.LoadFilterDecisions(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, filters, 1)
	runs, err := store.LoadSandboxRuns(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	findings, err := store.LoadFindings(ctx, "task-1", "finding", 10, 0)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	findings, err = store.LoadFindings(ctx, "task-1", "finding", 0, -10)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	artifacts, err := store.LoadArtifacts(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	metrics, err := store.LoadMetrics(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, 1, metrics.FindingCount)

	empty, err := store.LoadFindings(ctx, "task-1", "finding", 1, 1)
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = store.LoadTask(ctx, "missing")
	require.Error(t, err)
	_, err = store.LoadReport(ctx, "missing")
	require.Error(t, err)
	_, err = store.LoadMetrics(ctx, "missing")
	require.Error(t, err)
	_, err = store.LoadLatestTaskIDByDiffHash(ctx, "missing")
	require.Error(t, err)
}

func TestStoreDiffHashAliasTracksLatestTask(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	store, err := OpenStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	first := minimalReport("task-old")
	first.Task.DiffHash = "same-diff"
	first.Input.Hash = "same-diff"
	second := minimalReport("task-new")
	second.Task.DiffHash = "same-diff"
	second.Input.Hash = "same-diff"
	require.NoError(t, store.SaveReport(ctx, first, "old.json", "old.md"))
	require.NoError(t, store.SaveReport(ctx, second, "new.json", "new.md"))
	latest, err := store.LoadLatestTaskIDByDiffHash(ctx, "same-diff")
	require.NoError(t, err)
	require.Equal(t, "task-new", latest)
}

func TestStoreConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	store, err := OpenStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	var wg sync.WaitGroup
	for _, id := range []string{"task-a", "task-b", "task-c"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, store.SaveReport(ctx, minimalReport(id), id+".json", id+".md"))
		}()
	}
	wg.Wait()
}

func TestStoreLoadsZeroMetricsMapsAndHelpers(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	store, err := OpenStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	report := minimalReport("task-zero-metrics")
	report.Metrics = Metrics{}
	require.NoError(t, store.SaveReport(ctx, report, "report.json", "report.md"))
	metrics, err := store.LoadMetrics(ctx, report.Task.ID)
	require.NoError(t, err)
	require.NotNil(t, metrics.SeverityCounts)
	require.NotNil(t, metrics.ErrorCounts)

	require.Equal(t, 1, boolInt(true))
	require.Equal(t, 0, boolInt(false))
	require.Empty(t, formatTime(time.Time{}))
	require.True(t, parseTime("").IsZero())
	require.True(t, parseTime("not-time").IsZero())
}

func minimalReport(id string) ReviewReport {
	now := time.Now().UTC()
	return ReviewReport{
		Task: ReviewTask{
			ID:          id,
			InputKind:   "test",
			DiffHash:    "hash",
			Status:      taskStatusCompleted,
			StartedAt:   now,
			CompletedAt: now,
		},
		Input: DiffSummary{
			Hash:       "hash",
			Files:      []ChangedFile{},
			AddedLines: []AddedLine{},
			Packages:   []PackageInfo{},
		},
		Findings: []Finding{{
			Severity:       severityHigh,
			Category:       "security",
			File:           "a.go",
			Line:           1,
			Title:          "secret",
			Evidence:       "apiKey=[REDACTED]",
			Recommendation: "rotate",
			Confidence:     0.9,
			Source:         "test",
			RuleID:         "test.secret",
		}},
		Warnings:         []Finding{},
		NeedsHumanReview: []Finding{},
		PermissionSummary: []PermissionRecord{{
			TaskID:    id,
			ToolName:  reviewToolName,
			Command:   "go test ./...",
			Action:    "allow",
			CreatedAt: now,
		}},
		FilterSummary: []FilterRecord{{
			TaskID:    id,
			Filter:    "input.size_gate",
			Action:    "allow",
			CreatedAt: now,
		}},
		SandboxRuns: []SandboxRun{{
			TaskID:      id,
			Runtime:     "fake",
			Command:     "go test ./...",
			Status:      "completed",
			CompletedAt: now,
		}},
		Artifacts: []ArtifactRecord{{
			TaskID:    id,
			Name:      "review_report.json",
			Path:      "review_report.json",
			MIMEType:  "application/json",
			CreatedAt: now,
		}},
		Metrics:    Metrics{FindingCount: 1, SeverityCounts: map[string]int{severityHigh: 1}, ErrorCounts: map[string]int{}},
		Conclusion: "ok",
	}
}
