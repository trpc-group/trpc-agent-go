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

func TestRunEvaluationMeasuresFixtureQuality(t *testing.T) {
	out := t.TempDir()
	report, jsonPath, mdPath, err := RunEvaluation(context.Background(), ReviewOptions{
		FixtureDir:     "testdata/fixtures",
		OutDir:         out,
		Runtime:        "fake",
		DryRun:         true,
		SandboxTimeout: time.Second,
		OutputLimit:    1024,
		SkillsRoot:     "skills",
	}, "testdata/eval_labels.json")
	require.NoError(t, err)
	require.FileExists(t, jsonPath)
	require.FileExists(t, mdPath)
	require.Equal(t, 10, report.FixtureCount)
	require.Equal(t, 1.0, report.Recall)
	require.Equal(t, 1.0, report.HighRiskRecall)
	require.LessOrEqual(t, report.FalsePositiveRate, 0.15)
	require.Equal(t, 1.0, report.RedactionRate)
	require.True(t, report.PassedHiddenThreshold)
	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "AKID1234567890SECRET")
	require.NotContains(t, string(raw), `"false_positives": null`)
}

func TestRunEvaluationErrors(t *testing.T) {
	_, _, _, err := RunEvaluation(context.Background(), ReviewOptions{}, "missing.json")
	require.Error(t, err)
	empty := filepath.Join(t.TempDir(), "labels.json")
	require.NoError(t, os.WriteFile(empty, []byte(`{"fixtures":[]}`), 0o644))
	_, _, _, err = RunEvaluation(context.Background(), ReviewOptions{}, empty)
	require.Error(t, err)
}

func TestRunCLIEvaluation(t *testing.T) {
	out := t.TempDir()
	err := runCLI([]string{
		"--eval-labels", "testdata/eval_labels.json",
		"--fixture-dir", "testdata/fixtures",
		"--runtime", "fake",
		"--dry-run",
		"--out-dir", out,
		"--skills-root", "skills",
	})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(out, "eval_report.json"))
	require.FileExists(t, filepath.Join(out, "eval_report.md"))
}

func TestEvalHelpers(t *testing.T) {
	require.Equal(t, 1.0, ratio(0, 0))
	require.Equal(t, 0.5, ratio(1, 2))
	actual := map[string]struct{}{"a": {}, "c": {}}
	require.Equal(t, []string{"a"}, intersection([]string{"a", "b"}, actual))
	require.Equal(t, []string{"b"}, missing([]string{"a", "b"}, actual))
	require.Equal(t, []string{"a", "c"}, sortedKeys(actual))
}
