//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

func TestRunDeterministicExample(t *testing.T) {
	outputDir := t.TempDir()
	err := run(context.Background(), []string{
		"-config", "./data/promptiter-regression-loop-app/promptiter.json",
		"-output-dir", outputDir,
	})
	require.NoError(t, err)
	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	mdPath := filepath.Join(outputDir, "optimization_report.md")
	data, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var report regressionloop.Report
	require.NoError(t, json.Unmarshal(data, &report))
	assert.True(t, report.GateDecision.Accepted)
	require.Len(t, report.Candidates, 3)
	assert.False(t, report.Candidates[2].GateDecision.Accepted)
	assert.Contains(t, report.Candidates[2].GateDecision.FailedRules, "no_new_hard_fails")
	md, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(md), "Validation Delta")
	assert.Contains(t, string(md), "Candidate 3")
}

func TestFakeEvaluatorRejectsEmptyEvalSet(t *testing.T) {
	dir := t.TempDir()
	evalsetPath := filepath.Join(dir, "empty.evalset.json")
	require.NoError(t, os.WriteFile(evalsetPath, []byte(`{"evalSetId":"empty","evalCases":[]}`), 0o644))

	_, err := newFakeEvaluator().Evaluate(context.Background(), regressionloop.EvaluationRequest{
		Prompt:  "baseline",
		EvalSet: regressionloop.EvalSetRef{ID: "empty", Path: evalsetPath},
	})
	require.ErrorContains(t, err, "has no eval cases")
}
