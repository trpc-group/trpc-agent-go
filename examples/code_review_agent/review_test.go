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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunReviewFakeWritesReportsAndDB(t *testing.T) {
	opts := testOptions(t, "security")
	report, jsonPath, mdPath, err := RunReview(context.Background(), opts)
	require.NoError(t, err)
	require.FileExists(t, jsonPath)
	require.FileExists(t, mdPath)
	require.NotEmpty(t, report.Findings)
	store, err := OpenStore(context.Background(), opts.DBPath)
	require.NoError(t, err)
	defer store.Close()
	loaded, err := store.LoadReport(context.Background(), report.Task.ID)
	require.NoError(t, err)
	require.Equal(t, report.Task.ID, loaded.Task.ID)
	loadedRaw, err := json.Marshal(loaded)
	require.NoError(t, err)
	require.NotContains(t, string(loadedRaw), "AKID1234567890SECRET")
	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "AKID1234567890SECRET")
}

func TestRunReviewSandboxFailureDoesNotCrash(t *testing.T) {
	opts := testOptions(t, "sandbox_failure")
	report, _, _, err := RunReview(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, taskStatusFailed, report.Task.Status)
	require.NotEmpty(t, report.SandboxRuns)
}

func TestRunReviewSandboxSetupFailureIsRecorded(t *testing.T) {
	opts := testOptions(t, "sandbox_setup_failure")
	report, _, _, err := RunReview(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, taskStatusFailed, report.Task.Status)
	require.Len(t, report.SandboxRuns, 1)
	require.Equal(t, "sandbox_error", report.SandboxRuns[0].ErrorType)
	store, err := OpenStore(context.Background(), opts.DBPath)
	require.NoError(t, err)
	defer store.Close()
	runs, err := store.LoadSandboxRuns(context.Background(), report.Task.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "sandbox_error", runs[0].ErrorType)
}

func TestRunReviewLargeDiffSkipsSandboxAndPersistsGate(t *testing.T) {
	opts := testOptions(t, "security")
	opts.MaxDiffLines = 1
	report, _, _, err := RunReview(context.Background(), opts)
	require.NoError(t, err)
	require.Empty(t, report.SandboxRuns)
	require.NotEmpty(t, report.NeedsHumanReview)
	require.Len(t, report.FilterSummary, 1)
	require.Equal(t, "needs_human_review", report.FilterSummary[0].Action)
	require.Equal(t, "input.size_gate", report.NeedsHumanReview[len(report.NeedsHumanReview)-1].RuleID)
	store, err := OpenStore(context.Background(), opts.DBPath)
	require.NoError(t, err)
	defer store.Close()
	filters, err := store.LoadFilterDecisions(context.Background(), report.Task.ID)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	require.Equal(t, "needs_human_review", filters[0].Action)
}

func TestRunReviewErrorPaths(t *testing.T) {
	opts := testOptions(t, "security")
	opts.SkillsRoot = "missing-skills"
	_, _, _, err := RunReview(context.Background(), opts)
	require.Error(t, err)

	opts = testOptions(t, "missing-fixture")
	_, _, _, err = RunReview(context.Background(), opts)
	require.Error(t, err)

	opts = testOptions(t, "security")
	opts.Runtime = "unknown"
	_, _, _, err = RunReview(context.Background(), opts)
	require.Error(t, err)
}

func TestAllFixturesParse(t *testing.T) {
	names, err := fixtureNames("testdata/fixtures")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(names), 9)
	for _, name := range names {
		raw, err := loadFixture("testdata/fixtures", name)
		require.NoError(t, err, name)
		_, err = ParseUnifiedDiff(raw)
		require.NoError(t, err, name)
	}
}

func testOptions(t *testing.T, fixture string) ReviewOptions {
	t.Helper()
	out := t.TempDir()
	return ReviewOptions{
		Fixture:        fixture,
		FixtureDir:     "testdata/fixtures",
		OutDir:         out,
		DBPath:         filepath.Join(out, "review.db"),
		Runtime:        "fake",
		DryRun:         true,
		SandboxTimeout: time.Second,
		OutputLimit:    1024,
		SkillsRoot:     "skills",
	}
}
