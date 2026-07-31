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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluateAttributesFailure(t *testing.T) {
	set := evalSet{Name: "test", Cases: []evalCase{{
		ID: "route", Required: []string{"route=search"}, FailureCategory: "route_error",
	}}}
	result := evaluate(set, "format=json", metricConfig{PassScore: 1})
	require.Equal(t, 0.0, result.Score)
	require.Equal(t, "route_error", result.Attributions[0].Category)
	require.Contains(t, result.Attributions[0].Reason, "route=search")
	require.Contains(t, result.Cases[0].Trace, "missing:route=search")
}

func TestComputeDeltaClassifiesChanges(t *testing.T) {
	baseline := evaluationSummary{Cases: []caseResult{
		{ID: "fixed", Score: 0, Passed: false},
		{ID: "broken", Score: 1, Passed: true, Hard: true},
	}}
	candidate := evaluationSummary{Cases: []caseResult{
		{ID: "fixed", Score: 1, Passed: true},
		{ID: "broken", Score: 0, Passed: false, Hard: true},
	}}
	deltas := computeDelta(baseline, candidate)
	require.Equal(t, deltaNewPass, deltas[0].Status)
	require.Equal(t, deltaNewFailure, deltas[1].Status)
}

func TestGateRejectsOverfitAndAcceptsGeneralization(t *testing.T) {
	cfg := gateConfig{
		MinValidationGain: 0.1, ForbidNewFailures: true, NoHardRegression: true,
		MaxCalls: 10, MaxEstimatedTokens: 1000,
	}
	baseline := evaluationSummary{Score: 0.5}
	overfit := evaluationSummary{Score: 0.5}
	rejected := decideGate(cfg, baseline, overfit, []caseDelta{{
		CaseID: "held-out", Status: deltaNewFailure, Delta: -1,
	}}, costSummary{Calls: 6})
	require.False(t, rejected.Accepted)
	require.NotEmpty(t, rejected.Reasons)
	accepted := decideGate(cfg, baseline, evaluationSummary{Score: 1}, []caseDelta{{
		CaseID: "held-out", Status: deltaNewPass, Delta: 1,
	}}, costSummary{Calls: 6, EstimatedTokens: 100})
	require.True(t, accepted.Accepted)
}

func TestRunPipelineWritesAuditableReports(t *testing.T) {
	outputDir := t.TempDir()
	report, err := runPipeline(context.Background(), "data", outputDir)
	require.NoError(t, err)
	require.Equal(t, "generalized", report.AcceptedCandidateID)
	require.Len(t, report.Rounds, 3)
	require.False(t, report.Rounds[0].Gate.Accepted)
	require.False(t, report.Rounds[1].Gate.Accepted)
	require.True(t, report.Rounds[2].Gate.Accepted)
	data, err := os.ReadFile(filepath.Join(outputDir, "optimization_report.json"))
	require.NoError(t, err)
	var decoded optimizationReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "accept_candidate", decoded.Decision)
	markdown, err := os.ReadFile(filepath.Join(outputDir, "optimization_report.md"))
	require.NoError(t, err)
	require.Contains(t, string(markdown), "overfit")
}
