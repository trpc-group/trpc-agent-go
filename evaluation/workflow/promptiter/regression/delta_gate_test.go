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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeDeltaTransitions(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.6375,
		deltaTestCase("new-pass", 0.2, false, false),
		deltaTestCase("new-failure", 0.8, true, false),
		deltaTestCase("improved", 0.65, true, false),
		deltaTestCase("regressed", 0.9, true, false),
	)
	candidate := deltaTestSummary("validation", 0.6375,
		deltaTestCase("regressed", 0.8, true, false),
		deltaTestCase("improved", 0.75, true, false),
		deltaTestCase("new-failure", 0.1, false, false),
		deltaTestCase("new-pass", 0.9, true, false),
	)

	delta, err := ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	require.True(t, delta.Complete)
	assert.Equal(t, 1, delta.NewPasses)
	assert.Equal(t, 1, delta.NewFailures)
	assert.Equal(t, 2, delta.ScoreImprovements)
	assert.Equal(t, 2, delta.ScoreRegressions)
	assert.Equal(t, 0, delta.NewHardFails)
	require.Len(t, delta.Cases, 4)

	byID := deltaTestCasesByID(delta.Cases)
	assert.Equal(t, DeltaNewPass, byID["new-pass"].Outcome)
	assert.True(t, byID["new-pass"].BecamePassed)
	assert.True(t, byID["new-pass"].ScoreImproved)

	assert.Equal(t, DeltaNewFailure, byID["new-failure"].Outcome)
	assert.True(t, byID["new-failure"].BecameFailed)
	assert.True(t, byID["new-failure"].ScoreRegressed)

	assert.Equal(t, DeltaImproved, byID["improved"].Outcome)
	assert.False(t, byID["improved"].BecamePassed)
	assert.True(t, byID["improved"].ScoreImproved)

	assert.Equal(t, DeltaRegressed, byID["regressed"].Outcome)
	assert.False(t, byID["regressed"].BecameFailed)
	assert.True(t, byID["regressed"].ScoreRegressed)

	// ComputeDelta sorts by case ID so report output is independent of input order.
	assert.Equal(t, []string{"improved", "new-failure", "new-pass", "regressed"}, []string{
		delta.Cases[0].CaseID,
		delta.Cases[1].CaseID,
		delta.Cases[2].CaseID,
		delta.Cases[3].CaseID,
	})
}

func TestComputeDeltaMissingCandidateFailsClosed(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.8,
		deltaTestCase("kept", 0.8, true, false),
		deltaTestCase("missing", 0.8, true, false),
	)
	candidate := deltaTestSummary("validation", 0.9,
		deltaTestCase("kept", 0.9, true, false),
	)

	delta, err := ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	assert.False(t, delta.Complete)
	assert.Equal(t, 1, delta.NewFailures)
	assert.Equal(t, 1, delta.NewHardFails)
	assert.Contains(t, delta.CoverageIssues, "candidate is missing case missing")

	missing := deltaTestCasesByID(delta.Cases)["missing"]
	assert.Equal(t, DeltaMissingCandidate, missing.Outcome)
	assert.False(t, missing.CandidatePresent)
	assert.True(t, missing.CandidateHardFail)
	assert.True(t, missing.NewHardFail)

	decision, err := EvaluateGate(GatePolicy{
		MinValidationScoreGain: 0.05,
		RejectNewHardFails:     true,
	}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, deltaTestCheck(t, decision, "evaluation_comparable").Passed)
	assert.False(t, deltaTestCheck(t, decision, "no_new_hard_failures").Passed)
}

func TestComputeDeltaRejectsDuplicateAndNonFiniteScores(t *testing.T) {
	tests := []struct {
		name      string
		baseline  *EvaluationSummary
		candidate *EvaluationSummary
		wantError string
	}{
		{
			name: "duplicate_case",
			baseline: deltaTestSummary("validation", 0.5,
				deltaTestCase("duplicate", 0.5, true, false),
				deltaTestCase("duplicate", 0.5, true, false),
			),
			candidate: deltaTestSummary("validation", 0.6,
				deltaTestCase("duplicate", 0.6, true, false),
			),
			wantError: "duplicate case id",
		},
		{
			name: "nan_overall_score",
			baseline: deltaTestSummary("validation", math.NaN(),
				deltaTestCase("case", 0.5, true, false),
			),
			candidate: deltaTestSummary("validation", 0.6,
				deltaTestCase("case", 0.6, true, false),
			),
			wantError: "overall scores must be finite",
		},
		{
			name: "nan_case_score",
			baseline: deltaTestSummary("validation", 0.5,
				deltaTestCase("case", math.NaN(), true, false),
			),
			candidate: deltaTestSummary("validation", 0.6,
				deltaTestCase("case", 0.6, true, false),
			),
			wantError: "case \"case\" baseline: score must be finite",
		},
		{
			name: "nan_metric_score",
			baseline: deltaTestSummary("validation", 0.5, CaseResult{
				CaseID: "case",
				Score:  0.5,
				Passed: true,
				MetricResults: []MetricResult{{
					MetricName: "quality",
					Score:      math.NaN(),
					Threshold:  0.5,
					Weight:     1,
					Passed:     true,
				}},
			}),
			candidate: deltaTestSummary("validation", 0.6,
				deltaTestCase("case", 0.6, true, false),
			),
			wantError: "metric \"quality\": score must be finite",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delta, err := ComputeDelta(test.baseline, test.candidate)
			require.Error(t, err)
			assert.Nil(t, delta)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}

func TestComputeDeltaRejectsInvalidScoresOnMissingSide(t *testing.T) {
	baselineCase := deltaTestCase("missing", 0.5, false, false)
	baselineCase.MetricResults[0].Score = math.NaN()
	baseline := deltaTestSummary("validation", 0.5, baselineCase)
	candidate := deltaTestSummary("validation", 0.6, deltaTestCase("candidate-only", 0.6, true, false))
	_, err := ComputeDelta(baseline, candidate)
	require.ErrorContains(t, err, "metric \"quality\": score must be finite")

	baseline = deltaTestSummary("validation", 1.1, deltaTestCase("case", 0.5, false, false))
	candidate = deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false))
	_, err = ComputeDelta(baseline, candidate)
	require.ErrorContains(t, err, "in [0,1]")
}

func TestComputeDeltaFailsClosedWhenMetricConfigurationChanges(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	candidateCase := deltaTestCase("case", 0.7, true, false)
	candidateCase.MetricResults[0].Weight = 2
	candidate := deltaTestSummary("validation", 0.7, candidateCase)

	delta, err := ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	assert.False(t, delta.Complete)
	assert.Contains(t, delta.CoverageIssues, "case case metric quality configuration changed")
}

func TestEvaluateGateRejectsForgedWeightedCaseScore(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	candidateCase := deltaTestCase("case", 0.8, true, false)
	candidateCase.Score = 0.9
	candidate := deltaTestSummary("validation", 0.9, candidateCase)

	_, err := EvaluateGate(GatePolicy{}, GateInput{
		Delta:               &DeltaSummary{Complete: true},
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.ErrorContains(t, err, "does not match weighted metric score")
}

func TestEvaluateGateRejectsForgedPassStatus(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.5, deltaTestCase("case", 0.5, false, false))
	candidate := deltaTestSummary("validation", 0.6, deltaTestCase("case", 0.6, true, false))
	candidate.Cases[0].Score = 0.4
	candidate.Cases[0].MetricResults[0].Score = 0.4
	candidate.Cases[0].MetricResults[0].Passed = false
	candidate.OverallScore = 0.4
	candidate.Cases[0].Passed = true

	_, err := EvaluateGate(GatePolicy{}, GateInput{
		Delta:               &DeltaSummary{Complete: true},
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.ErrorContains(t, err, "pass status true does not match score")
}

func TestComputeDeltaRejectsPassThresholdMismatch(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	candidate := deltaTestSummary("validation", 0.8, deltaTestCase("case", 0.8, true, false))
	candidate.PassThreshold = 0.75

	_, err := ComputeDelta(baseline, candidate)
	require.ErrorContains(t, err, "pass threshold mismatch")
}

func TestEvaluateGateRejectsPositiveScoreAfterExecutionError(t *testing.T) {
	failedCase := func(score float64) CaseResult {
		return CaseResult{
			CaseID:   "case",
			Score:    score,
			Passed:   false,
			HardFail: true,
			Error:    "model unavailable",
			MetricResults: []MetricResult{{
				MetricName: "quality",
				Score:      score,
				Threshold:  0,
				Weight:     1,
				Passed:     false,
			}},
		}
	}
	baseline := deltaTestSummary("validation", 0, failedCase(0))
	candidate := deltaTestSummary("validation", 1, failedCase(1))

	_, err := EvaluateGate(GatePolicy{}, GateInput{
		Delta:               &DeltaSummary{Complete: true},
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.ErrorContains(t, err, "score must be zero after an execution error")
}

func TestEvaluateGateAcceptsExactScoreGainThreshold(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7,
		deltaTestCase("case", 0.7, true, false),
	)
	candidate := deltaTestSummary("validation", 0.75,
		deltaTestCase("case", 0.75, true, false),
	)
	delta := deltaTestDelta(t, baseline, candidate)

	decision, err := EvaluateGate(GatePolicy{MinValidationScoreGain: 0.05}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.True(t, decision.Accepted)
	assert.True(t, deltaTestCheck(t, decision, "min_validation_score_gain").Passed)
}

func TestEvaluateGateRejectsOverallGainWithNewHardFailure(t *testing.T) {
	baselineHardCase := deltaTestCase("hard-regression", 1, true, false)
	baselineHardCase.MetricResults[0].Threshold = 1
	baselineHardCase.MetricResults[0].HardFail = true
	baseline := deltaTestSummary("validation", 0.6,
		deltaTestCase("large-gain", 0.2, false, false),
		baselineHardCase,
	)
	candidate := deltaTestSummary("validation", 0.85,
		deltaTestCase("large-gain", 0.9, true, false),
		deltaTestCase("hard-regression", 0.8, false, true),
	)
	delta := deltaTestDelta(t, baseline, candidate)
	require.Equal(t, 1, delta.NewHardFails)
	require.Greater(t, delta.ScoreDelta, 0.0)

	decision, err := EvaluateGate(GatePolicy{
		MinValidationScoreGain: 0.1,
		RejectNewHardFails:     true,
	}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.True(t, deltaTestCheck(t, decision, "min_validation_score_gain").Passed)
	assert.False(t, deltaTestCheck(t, decision, "no_new_hard_failures").Passed)
}

func TestEvaluateGateRejectsCriticalCaseRegressionDespiteOverallGain(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.65,
		deltaTestCase("ordinary", 0.4, false, false),
		deltaTestCase("critical", 0.9, true, false),
	)
	candidate := deltaTestSummary("validation", 0.825,
		deltaTestCase("ordinary", 0.8, true, false),
		deltaTestCase("critical", 0.85, true, false),
	)
	delta := deltaTestDelta(t, baseline, candidate)

	decision, err := EvaluateGate(GatePolicy{
		MinValidationScoreGain: 0.1,
		CriticalCaseIDs:        []string{"critical"},
		MaxCriticalScoreDrop:   0.01,
	}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.True(t, deltaTestCheck(t, decision, "min_validation_score_gain").Passed)
	criticalCheck := deltaTestCheck(t, decision, "critical_cases_non_regression")
	assert.False(t, criticalCheck.Passed)
	assert.InDelta(t, 0.05, criticalCheck.Actual, scoreEpsilon)
}

func TestEvaluateGateBudgetBoundaries(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7,
		deltaTestCase("case", 0.7, true, false),
	)
	candidate := deltaTestSummary("validation", 0.8,
		deltaTestCase("case", 0.8, true, false),
	)
	delta := deltaTestDelta(t, baseline, candidate)
	maxCost := 0.25
	maxModelCalls := 3
	maxTotalCalls := 5
	maxLatency := int64(100)
	policy := GatePolicy{
		MinValidationScoreGain: 0.1,
		MaxCostUSD:             &maxCost,
		MaxModelCalls:          &maxModelCalls,
		MaxTotalCalls:          &maxTotalCalls,
		MaxLatencyMS:           &maxLatency,
	}

	tests := []struct {
		name            string
		usage           Usage
		wantAccepted    bool
		failedCheckName string
	}{
		{
			name:         "exact_limits_are_accepted",
			usage:        Usage{ModelCalls: 3, ToolCalls: 2, CostUSD: 0.25, LatencyMS: 100},
			wantAccepted: true,
		},
		{
			name:            "cost_over_budget",
			usage:           Usage{ModelCalls: 3, ToolCalls: 2, CostUSD: 0.250001, LatencyMS: 100},
			failedCheckName: "max_cost_usd",
		},
		{
			name:            "model_calls_over_budget",
			usage:           Usage{ModelCalls: 4, ToolCalls: 1, CostUSD: 0.25, LatencyMS: 100},
			failedCheckName: "max_model_calls",
		},
		{
			name:            "total_calls_over_budget",
			usage:           Usage{ModelCalls: 3, ToolCalls: 3, CostUSD: 0.25, LatencyMS: 100},
			failedCheckName: "max_total_calls",
		},
		{
			name:            "latency_over_budget",
			usage:           Usage{ModelCalls: 3, ToolCalls: 2, CostUSD: 0.25, LatencyMS: 101},
			failedCheckName: "max_latency_ms",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := EvaluateGate(policy, GateInput{
				Delta:               delta,
				BaselineValidation:  baseline,
				CandidateValidation: candidate,
				CandidateUsage:      test.usage,
				BaselinePromptHash:  "baseline",
				CandidatePromptHash: "candidate",
			})
			require.NoError(t, err)
			assert.Equal(t, test.wantAccepted, decision.Accepted)
			if test.failedCheckName != "" {
				assert.False(t, deltaTestCheck(t, decision, test.failedCheckName).Passed)
			}
		})
	}
}

func TestEvaluateGateRejectsInconsistentProvidedDelta(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	candidate := deltaTestSummary("validation", 0.8, deltaTestCase("case", 0.8, true, false))
	delta := deltaTestDelta(t, baseline, candidate)
	delta.ScoreDelta = 1

	decision, err := EvaluateGate(GatePolicy{MinValidationScoreGain: 0.05}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, deltaTestCheck(t, decision, "delta_consistent").Passed)
	assert.InDelta(t, 0.1, deltaTestCheck(t, decision, "min_validation_score_gain").Actual, scoreEpsilon)
}

func TestEvaluateGateRejectsEmptyEvaluationAndNegativeUsage(t *testing.T) {
	empty := &EvaluationSummary{EvalSetID: "validation"}
	_, err := EvaluateGate(GatePolicy{}, GateInput{
		Delta:               &DeltaSummary{Complete: true},
		BaselineValidation:  empty,
		CandidateValidation: empty,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.ErrorContains(t, err, "evaluation has no cases")

	baseline := deltaTestSummary("validation", 0.7, deltaTestCase("case", 0.7, true, false))
	candidate := deltaTestSummary("validation", 0.8, deltaTestCase("case", 0.8, true, false))
	delta := deltaTestDelta(t, baseline, candidate)
	_, err = EvaluateGate(GatePolicy{}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		CandidateUsage:      Usage{ModelCalls: -1},
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.ErrorContains(t, err, "model calls cannot be negative")
}

func TestEvaluateGateDetectsSubMicroCriticalRegression(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.5,
		deltaTestCase("critical", 0.8, true, false),
		deltaTestCase("ordinary", 0.2, false, false),
	)
	candidate := deltaTestSummary("validation", 0.6,
		deltaTestCase("critical", 0.7999996, true, false),
		deltaTestCase("ordinary", 0.4000004, false, false),
	)
	delta := deltaTestDelta(t, baseline, candidate)
	require.Less(t, deltaTestCasesByID(delta.Cases)["critical"].ScoreDelta, 0.0)

	decision, err := EvaluateGate(GatePolicy{
		MinValidationScoreGain: 0.05,
		CriticalCaseIDs:        []string{"critical"},
		MaxCriticalScoreDrop:   0,
	}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "baseline",
		CandidatePromptHash: "candidate",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, deltaTestCheck(t, decision, "critical_cases_non_regression").Passed)
}

func TestEvaluateGateRejectsPromptNoOp(t *testing.T) {
	baseline := deltaTestSummary("validation", 0.7,
		deltaTestCase("case", 0.7, true, false),
	)
	candidate := deltaTestSummary("validation", 0.8,
		deltaTestCase("case", 0.8, true, false),
	)
	delta := deltaTestDelta(t, baseline, candidate)

	decision, err := EvaluateGate(GatePolicy{MinValidationScoreGain: 0.1}, GateInput{
		Delta:               delta,
		BaselineValidation:  baseline,
		CandidateValidation: candidate,
		BaselinePromptHash:  "same-prompt",
		CandidatePromptHash: "same-prompt",
	})
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, deltaTestCheck(t, decision, "prompt_changed").Passed)
	assert.True(t, deltaTestCheck(t, decision, "min_validation_score_gain").Passed)
}

func deltaTestCase(id string, score float64, passed bool, hardFail bool) CaseResult {
	metricThreshold := 0.5
	if hardFail {
		metricThreshold = 1
	}
	return CaseResult{
		CaseID:   id,
		Score:    score,
		Passed:   passed,
		HardFail: hardFail,
		MetricResults: []MetricResult{
			{
				MetricName: "quality",
				Score:      score,
				Threshold:  metricThreshold,
				Weight:     1,
				Passed:     scorePasses(score, metricThreshold),
				HardFail:   hardFail,
			},
		},
	}
}

func deltaTestSummary(evalSetID string, score float64, cases ...CaseResult) *EvaluationSummary {
	return &EvaluationSummary{
		EvalSetID:     evalSetID,
		PassThreshold: 0.500001,
		OverallScore:  score,
		Cases:         cases,
	}
}

func deltaTestDelta(t *testing.T, baseline, candidate *EvaluationSummary) *DeltaSummary {
	t.Helper()
	delta, err := ComputeDelta(baseline, candidate)
	require.NoError(t, err)
	require.True(t, delta.Complete)
	return delta
}

func deltaTestCasesByID(cases []CaseDelta) map[string]CaseDelta {
	result := make(map[string]CaseDelta, len(cases))
	for _, evalCase := range cases {
		result[evalCase.CaseID] = evalCase
	}
	return result
}

func deltaTestCheck(t *testing.T, decision GateDecision, name string) GateCheck {
	t.Helper()
	for _, check := range decision.Checks {
		if check.Name == name {
			return check
		}
	}
	require.FailNow(t, "gate check not found", "name=%s", name)
	return GateCheck{}
}
