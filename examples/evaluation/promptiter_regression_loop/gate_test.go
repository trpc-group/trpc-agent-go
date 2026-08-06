//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func testEvalResult(overall float64, cases ...CaseScore) *EvalResult {
	return &EvalResult{OverallScore: overall, Cases: cases}
}

func TestGate_AcceptsWhenScoreGainMet(t *testing.T) {
	baseline := testEvalResult(0.0,
		CaseScore{EvalCaseID: "v1", Passed: false, Score: 0.0},
		CaseScore{EvalCaseID: "v2", Passed: false, Score: 0.0},
	)
	candidate := testEvalResult(1.0,
		CaseScore{EvalCaseID: "v1", Passed: true, Score: 1.0},
		CaseScore{EvalCaseID: "v2", Passed: true, Score: 1.0},
	)
	deltas := ComputeDeltas(baseline.Cases, candidate.Cases)
	decision := EvaluateGate(GateConfig{MinScoreGain: 0.05}, baseline, candidate, deltas, 10, 100)
	require.True(t, decision.Accepted)
	require.Contains(t, decision.Reason, "passed")
}

func TestGate_RejectsOverfitWhenValidationRegresses(t *testing.T) {
	// Train improved but the validation set regressed against the accepted baseline.
	accepted := testEvalResult(1.0,
		CaseScore{EvalCaseID: "v1", Passed: true, Score: 1.0},
		CaseScore{EvalCaseID: "v2", Passed: true, Score: 1.0},
	)
	overfit := testEvalResult(0.5,
		CaseScore{EvalCaseID: "v1", Passed: false, Score: 0.5},
		CaseScore{EvalCaseID: "v2", Passed: true, Score: 0.5},
	)
	deltas := ComputeDeltas(accepted.Cases, overfit.Cases)
	decision := EvaluateGate(GateConfig{MinScoreGain: 0.05}, accepted, overfit, deltas, 10, 100)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.Reason, "validation_score_gain")
	// The previously passing v1 must be flagged as a new hard fail.
	require.Contains(t, decision.Reason, "no_new_hard_fail")
}

func TestGate_RejectsNewHardFail(t *testing.T) {
	baseline := testEvalResult(0.5,
		CaseScore{EvalCaseID: "v1", Passed: true, Score: 1.0},
		CaseScore{EvalCaseID: "v2", Passed: false, Score: 0.0},
	)
	candidate := testEvalResult(0.5,
		CaseScore{EvalCaseID: "v1", Passed: false, Score: 0.0},
		CaseScore{EvalCaseID: "v2", Passed: true, Score: 1.0},
	)
	deltas := ComputeDeltas(baseline.Cases, candidate.Cases)
	// Same overall score, but v1 turned from passing to failing.
	decision := EvaluateGate(GateConfig{MinScoreGain: 0.0, MaxNewHardFails: 0}, baseline, candidate, deltas, 10, 100)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.Reason, "no_new_hard_fail")
}

func TestGate_RejectsKeyCaseRegression(t *testing.T) {
	baseline := testEvalResult(0.5,
		CaseScore{EvalCaseID: "key", Passed: true, Score: 1.0},
		CaseScore{EvalCaseID: "other", Passed: false, Score: 0.0},
	)
	candidate := testEvalResult(0.75,
		CaseScore{EvalCaseID: "key", Passed: true, Score: 0.4},
		CaseScore{EvalCaseID: "other", Passed: true, Score: 1.0},
	)
	deltas := ComputeDeltas(baseline.Cases, candidate.Cases)
	decision := EvaluateGate(GateConfig{
		MinScoreGain: 0.05,
		KeyCaseIDs:   []string{"key"},
	}, baseline, candidate, deltas, 10, 100)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.Reason, "key_cases_no_regression")
}

func TestGate_RejectsBudgetExceeded(t *testing.T) {
	baseline := testEvalResult(0.0)
	candidate := testEvalResult(1.0)
	decision := EvaluateGate(GateConfig{
		MinScoreGain:  0.05,
		MaxModelCalls: 5,
		MaxLatencyMs:  100,
	}, baseline, candidate, nil, 10, 200)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.Reason, "budget_within_limit")
}

func TestGate_AllChecksReported(t *testing.T) {
	baseline := testEvalResult(0.0,
		CaseScore{EvalCaseID: "v1", Passed: false, Score: 0.0},
	)
	candidate := testEvalResult(1.0,
		CaseScore{EvalCaseID: "v1", Passed: true, Score: 1.0},
	)
	deltas := ComputeDeltas(baseline.Cases, candidate.Cases)
	decision := EvaluateGate(GateConfig{MinScoreGain: 0.05, MaxNewHardFails: 0, MaxModelCalls: 10, MaxLatencyMs: 1000}, baseline, candidate, deltas, 5, 50)
	require.True(t, decision.Accepted)
	require.Len(t, decision.Checks, 4)
	require.Equal(t, "validation_score_gain", decision.Checks[0].Name)
	require.Equal(t, "no_new_hard_fail", decision.Checks[1].Name)
	require.Equal(t, "key_cases_no_regression", decision.Checks[2].Name)
	require.Equal(t, "budget_within_limit", decision.Checks[3].Name)
}
