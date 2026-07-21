//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestDecideAcceptsSafeImprovement(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("stable", 0.6, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("stable", 0.8, status.EvalStatusPassed))
	decision, err := Decide(GateConfig{MinValidationScoreGain: 0.1, RejectNewFailures: true}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.True(t, decision.Accepted)
	assert.InDelta(t, 0.2, decision.ScoreDelta, scoreEpsilon)
	assert.Equal(t, []string{"candidate satisfies all regression gates"}, decision.Reasons)
}

func TestDecideAccumulatesRegressionTraceAndBudgetReasons(t *testing.T) {
	original := evaluationWithCases(
		caseWithMetric("critical", 0.9, status.EvalStatusPassed),
		caseWithMetric("stable", 0.8, status.EvalStatusPassed),
	)
	accepted := evaluationWithCases(
		caseWithMetric("critical", 1.0, status.EvalStatusPassed),
		caseWithMetric("stable", 0.9, status.EvalStatusPassed),
	)
	candidate := evaluationWithCases(
		caseWithMetric("critical", 0.7, status.EvalStatusFailed),
		caseWithMetric("stable", 0.9, status.EvalStatusPassed),
	)
	candidate.Cases[0].Trace.Status = "incomplete"
	candidate.Usage = UsageSummary{TotalTokens: 11, ModelCalls: 3, ToolCalls: 2}
	config := GateConfig{
		MinValidationScoreGain: 0.2, RejectNewFailures: true,
		CriticalCaseIDs: []string{"critical"}, MaxCriticalScoreDrop: 0.1,
		MaxValidationTokens: 10, MaxValidationModelCalls: 2, MaxValidationToolCalls: 1,
	}

	decision, err := Decide(config, GateInput{
		OriginalBaseline: original, AcceptedBaseline: accepted, Candidate: candidate,
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	reasons := strings.Join(decision.Reasons, "\n")
	for _, want := range []string{
		reasonScoreGain, reasonNewFailure, reasonCriticalDrop,
		reasonTokenBudget, reasonModelCallBudget, reasonToolCallBudget,
		"candidate trace is incomplete",
	} {
		assert.Contains(t, reasons, want)
	}
}

func TestDecideRejectsInvalidCandidateWithoutReturningError(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("b", 1, status.EvalStatusPassed))
	decision, err := Decide(GateConfig{}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, strings.Join(decision.Reasons, " "), "candidate data is invalid")
}

func TestDecideTreatsZeroBudgetsAsUnlimited(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("a", 0.6, status.EvalStatusPassed))
	candidate.Usage = UsageSummary{TotalTokens: 100, ModelCalls: 20, ToolCalls: 10}
	decision, err := Decide(GateConfig{}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.True(t, decision.Accepted)
}

func TestDecideRejectsCriticalMetricDropEvenWhenCaseScoreIsStable(t *testing.T) {
	baselineCase := caseWithTwoMetrics("critical", "accuracy", "safety")
	candidateCase := baselineCase
	candidateCase.Metrics = []MetricResult{
		{Name: "accuracy", Score: 0.8, Status: status.EvalStatusPassed},
		{Name: "safety", Score: 1.2, Status: status.EvalStatusPassed},
	}
	baseline := evaluationWithCases(baselineCase)
	candidate := evaluationWithCases(candidateCase)
	decision, err := Decide(GateConfig{CriticalCaseIDs: []string{"critical"}}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, strings.Join(decision.Reasons, " "), reasonCriticalDrop)
}

func TestDecideRejectsMetricNewFailureInsideAlreadyFailedCase(t *testing.T) {
	baselineCase := caseWithTwoMetrics("mixed", "already_failed", "was_passing")
	baselineCase.Metrics[0].Score = 0
	baselineCase.Metrics[0].Status = status.EvalStatusFailed
	baselineCase.Score = 0.5
	baselineCase.Passed = false
	candidateCase := baselineCase
	candidateCase.Metrics = []MetricResult{
		{Name: "already_failed", Score: 1, Status: status.EvalStatusPassed},
		{Name: "was_passing", Score: 0, Status: status.EvalStatusFailed},
	}
	decision, err := Decide(GateConfig{RejectNewFailures: true}, GateInput{
		OriginalBaseline: evaluationWithCases(baselineCase),
		AcceptedBaseline: evaluationWithCases(baselineCase),
		Candidate:        evaluationWithCases(candidateCase),
	})

	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, reasonNewFailure+": mixed/was_passing")
}

func TestDecideProtectsPassAddedByAcceptedProfile(t *testing.T) {
	original := evaluationWithCases(caseWithMetric("case", 0, status.EvalStatusFailed))
	accepted := evaluationWithCases(caseWithMetric("case", 1, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("case", 0, status.EvalStatusFailed))
	decision, err := Decide(GateConfig{RejectNewFailures: true}, GateInput{
		OriginalBaseline: original, AcceptedBaseline: accepted, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, strings.Join(decision.Reasons, " "), reasonNewFailure)
}

func TestDecideRejectsNotEvaluatedCandidateWithAuditReason(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("case", 1, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("case", 0, status.EvalStatusNotEvaluated))
	decision, err := Decide(GateConfig{}, GateInput{
		OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: candidate,
	})

	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, reasonNotEvaluated+": case/quality")
}

func TestDecideReturnsErrorForInvalidBaselines(t *testing.T) {
	valid := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	_, err := Decide(GateConfig{}, GateInput{AcceptedBaseline: valid, Candidate: valid})
	require.ErrorContains(t, err, "original baseline evaluation is nil")
	_, err = Decide(GateConfig{}, GateInput{OriginalBaseline: valid, Candidate: valid})
	require.ErrorContains(t, err, "accepted baseline evaluation is nil")
}

func TestDecideRejectsUnknownCriticalCaseConfiguration(t *testing.T) {
	valid := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	_, err := Decide(GateConfig{CriticalCaseIDs: []string{"missing"}}, GateInput{
		OriginalBaseline: valid, AcceptedBaseline: valid, Candidate: valid,
	})
	require.ErrorContains(t, err, "not in original baseline")
}

func TestDecideRejectsInvalidConfig(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	tests := []struct {
		name   string
		config GateConfig
	}{
		{name: "nan gain", config: GateConfig{MinValidationScoreGain: math.NaN()}},
		{name: "negative gain", config: GateConfig{MinValidationScoreGain: -1}},
		{name: "negative drop", config: GateConfig{MaxCriticalScoreDrop: -1}},
		{name: "negative tokens", config: GateConfig{MaxValidationTokens: -1}},
		{name: "negative model calls", config: GateConfig{MaxValidationModelCalls: -1}},
		{name: "negative tool calls", config: GateConfig{MaxValidationToolCalls: -1}},
		{name: "empty critical", config: GateConfig{CriticalCaseIDs: []string{" "}}},
		{name: "duplicate critical", config: GateConfig{CriticalCaseIDs: []string{"a", "a"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decide(test.config, GateInput{
				OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: baseline,
			})
			require.Error(t, err)
		})
	}
}

func TestGateDecisionMatchesExpectedOutcome(t *testing.T) {
	for _, test := range gateAccuracyCases() {
		t.Run(test.name, func(t *testing.T) {
			decision, err := Decide(test.config, test.input)
			require.NoError(t, err)
			assert.Equal(t, test.accepted, decision.Accepted)
		})
	}
}

type gateAccuracyCase struct {
	name     string
	config   GateConfig
	input    GateInput
	accepted bool
}

func gateAccuracyCases() []gateAccuracyCase {
	basePass := evaluationWithCases(caseWithMetric("case", 0.6, status.EvalStatusPassed))
	baseFail := evaluationWithCases(caseWithMetric("case", 0.4, status.EvalStatusFailed))
	improved := evaluationWithCases(caseWithMetric("case", 0.8, status.EvalStatusPassed))
	declined := evaluationWithCases(caseWithMetric("case", 0.5, status.EvalStatusFailed))
	expensive := evaluationWithCases(caseWithMetric("case", 0.8, status.EvalStatusPassed))
	expensive.Usage.TotalTokens = 101
	criticalBaseline := evaluationWithCases(
		caseWithMetric("case", 0.6, status.EvalStatusPassed),
		caseWithMetric("other", 0.4, status.EvalStatusFailed),
	)
	criticalWithinTolerance := evaluationWithCases(
		caseWithMetric("case", 0.5, status.EvalStatusPassed),
		caseWithMetric("other", 0.7, status.EvalStatusPassed),
	)
	return []gateAccuracyCase{
		{name: "safe improvement", config: GateConfig{MinValidationScoreGain: 0.1}, input: GateInput{basePass, basePass, improved}, accepted: true},
		{name: "gain too small", config: GateConfig{MinValidationScoreGain: 0.3}, input: GateInput{basePass, basePass, improved}, accepted: false},
		{name: "new failure", config: GateConfig{RejectNewFailures: true}, input: GateInput{basePass, basePass, declined}, accepted: false},
		{name: "new pass", config: GateConfig{}, input: GateInput{baseFail, baseFail, improved}, accepted: true},
		{name: "critical decline", config: GateConfig{CriticalCaseIDs: []string{"case"}}, input: GateInput{basePass, basePass, declined}, accepted: false},
		{name: "critical tolerance", config: GateConfig{CriticalCaseIDs: []string{"case"}, MaxCriticalScoreDrop: 0.2}, input: GateInput{criticalBaseline, criticalBaseline, criticalWithinTolerance}, accepted: true},
		{name: "token budget", config: GateConfig{MaxValidationTokens: 100}, input: GateInput{basePass, basePass, expensive}, accepted: false},
		{name: "zero budget", config: GateConfig{}, input: GateInput{basePass, basePass, expensive}, accepted: true},
		{name: "unchanged allowed", config: GateConfig{}, input: GateInput{basePass, basePass, basePass}, accepted: true},
		{name: "unchanged below gain", config: GateConfig{MinValidationScoreGain: 0.01}, input: GateInput{basePass, basePass, basePass}, accepted: false},
	}
}
