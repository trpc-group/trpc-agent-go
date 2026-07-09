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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strictGate() GateConfig {
	return GateConfig{
		MinValidationScoreGain: 0.02,
		MaxNewHardFails:        0,
		MaxRegressedCases:      0,
		ProtectedCases:         []string{"val_protected"},
		HardFailCategories:     defaultHardFailCategories(),
	}
}

func passingCandidate(round int, score float64) Candidate {
	return Candidate{
		Round:           round,
		ValidationScore: score,
		Deltas: []CaseDelta{
			{EvalCaseID: "val_a", Kind: DeltaNewPass, EvalSetID: "validation"},
			{EvalCaseID: "val_protected", Kind: DeltaUnchanged, EvalSetID: "validation"},
		},
	}
}

func gateInput(gate GateConfig, candidates ...Candidate) GateInput {
	return GateInput{
		Gate:                    gate,
		BaselineValidationScore: 0.6,
		BaselineTrainScore:      0.5,
		Candidates:              candidates,
	}
}

func ruleByName(t *testing.T, rules []RuleOutcome, name string) RuleOutcome {
	t.Helper()
	for _, rule := range rules {
		if rule.Name == name {
			return rule
		}
	}
	t.Fatalf("rule %s not found", name)
	return RuleOutcome{}
}

func TestGateAcceptsCleanCandidate(t *testing.T) {
	decision, err := EvaluateGate(gateInput(strictGate(), passingCandidate(1, 0.8)))
	require.NoError(t, err)
	assert.True(t, decision.Accepted)
	assert.Equal(t, 1, decision.SelectedRound)
	assert.Equal(t, RecommendationAcceptPendingCanary, decision.Recommendation)
	assert.True(t, decision.Selection[0].Selected)
	for _, rule := range decision.Rules {
		assert.True(t, rule.Passed, rule.Name)
	}
}

func TestGateRejectsInsufficientScoreGain(t *testing.T) {
	decision, err := EvaluateGate(gateInput(strictGate(), passingCandidate(1, 0.61)))
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.Equal(t, RecommendationReject, decision.Recommendation)
	assert.False(t, ruleByName(t, decision.Rules, "min_validation_score_gain").Passed)
}

// TestGateRejectsNewHardFailDespiteScoreGain: the aggregate score clears the
// threshold but a new hard failure appears — the trade is forbidden.
func TestGateRejectsNewHardFailDespiteScoreGain(t *testing.T) {
	candidate := passingCandidate(1, 0.9)
	candidate.Deltas = append(candidate.Deltas, CaseDelta{
		EvalCaseID: "val_tool",
		EvalSetID:  "validation",
		Kind:       DeltaNewFail,
		CandidateAttribution: &CaseAttribution{
			RootCauses: []FailureCause{{Category: CauseToolCallError}},
		},
	})
	gate := strictGate()
	gate.MaxRegressedCases = 5 // isolate the hard-fail rule
	decision, err := EvaluateGate(gateInput(gate, candidate))
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, ruleByName(t, decision.Rules, "max_new_hard_fails").Passed)
	assert.Contains(t, ruleByName(t, decision.Rules, "max_new_hard_fails").Reason, "val_tool")
}

// TestGateSoftNewFailIsNotHard: a new fail whose root cause is outside the
// hard-fail category set only counts against max_regressed_cases.
func TestGateSoftNewFailIsNotHard(t *testing.T) {
	candidate := passingCandidate(1, 0.9)
	candidate.Deltas = append(candidate.Deltas, CaseDelta{
		EvalCaseID: "val_soft",
		EvalSetID:  "validation",
		Kind:       DeltaNewFail,
		CandidateAttribution: &CaseAttribution{
			RootCauses: []FailureCause{{Category: CauseFinalResponseMismatch}},
		},
	})
	gate := strictGate()
	gate.MaxRegressedCases = 1
	decision, err := EvaluateGate(gateInput(gate, candidate))
	require.NoError(t, err)
	assert.True(t, ruleByName(t, decision.Rules, "max_new_hard_fails").Passed)
	assert.True(t, decision.Accepted)
}

// TestGateUnattributedNewFailIsConservativelyHard: a new fail without
// attribution counts as hard.
func TestGateUnattributedNewFailIsConservativelyHard(t *testing.T) {
	candidate := passingCandidate(1, 0.9)
	candidate.Deltas = append(candidate.Deltas, CaseDelta{
		EvalCaseID: "val_mystery",
		EvalSetID:  "validation",
		Kind:       DeltaNewFail,
	})
	gate := strictGate()
	gate.MaxRegressedCases = 5
	decision, err := EvaluateGate(gateInput(gate, candidate))
	require.NoError(t, err)
	assert.False(t, ruleByName(t, decision.Rules, "max_new_hard_fails").Passed)
}

func TestGateRejectsProtectedCaseRegression(t *testing.T) {
	candidate := passingCandidate(1, 0.9)
	candidate.Deltas[1] = CaseDelta{
		EvalCaseID: "val_protected",
		EvalSetID:  "validation",
		Kind:       DeltaRegressed,
	}
	gate := strictGate()
	gate.MaxRegressedCases = 5
	decision, err := EvaluateGate(gateInput(gate, candidate))
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, ruleByName(t, decision.Rules, "protected_cases").Passed)
}

func TestGateRejectsOverBudget(t *testing.T) {
	gate := strictGate()
	gate.MaxModelCalls = 10
	gate.MaxWallClock = "1s"
	input := gateInput(gate, passingCandidate(1, 0.9))
	input.TotalModelCalls = 11
	input.TotalWallClock = 2 * time.Second
	decision, err := EvaluateGate(input)
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.False(t, ruleByName(t, decision.Rules, "max_model_calls").Passed)
	assert.False(t, ruleByName(t, decision.Rules, "max_wall_clock").Passed)
}

// TestGateOverfittingRejection is the direct test for acceptance criterion 3:
// train improves, aggregate validation improves, but a protected validation
// case flips to fail — the candidate must be rejected and the summary must
// name the overfitting pattern.
func TestGateOverfittingRejection(t *testing.T) {
	candidate := Candidate{
		Round:           1,
		ValidationScore: 0.83,
		TrainScore:      0.67,
		TrainScoreKnown: true,
		Deltas: []CaseDelta{
			{EvalCaseID: "val_gen", EvalSetID: "validation", Kind: DeltaNewPass},
			{
				EvalCaseID: "val_protected",
				EvalSetID:  "validation",
				Kind:       DeltaNewFail,
				CandidateAttribution: &CaseAttribution{
					RootCauses: []FailureCause{{Category: CauseFinalResponseMismatch}},
				},
			},
			{EvalCaseID: "val_stable", EvalSetID: "validation", Kind: DeltaUnchanged},
		},
	}
	decision, err := EvaluateGate(gateInput(strictGate(), candidate))
	require.NoError(t, err)
	assert.False(t, decision.Accepted)
	assert.True(t, ruleByName(t, decision.Rules, "min_validation_score_gain").Passed,
		"aggregate score gain passes — only per-case rules catch the overfit")
	assert.False(t, ruleByName(t, decision.Rules, "protected_cases").Passed)
	assert.Contains(t, decision.Summary, "过拟合")
	assert.Contains(t, decision.Summary, "val_protected")
}

func TestGateRequireTrainNotWorse(t *testing.T) {
	gate := strictGate()
	gate.RequireTrainNotWorse = true

	worse := passingCandidate(1, 0.9)
	worse.TrainScore = 0.4
	worse.TrainScoreKnown = true
	decision, err := EvaluateGate(gateInput(gate, worse))
	require.NoError(t, err)
	assert.False(t, ruleByName(t, decision.Rules, "train_not_worse").Passed)

	unknown := passingCandidate(1, 0.9)
	decision, err = EvaluateGate(gateInput(gate, unknown))
	require.NoError(t, err)
	outcome := ruleByName(t, decision.Rules, "train_not_worse")
	assert.False(t, outcome.Passed, "unknown train score fails safe")
	assert.Equal(t, "unknown", outcome.Observed)
}

func TestGateBestScoreSelection(t *testing.T) {
	decision, err := EvaluateGate(gateInput(strictGate(),
		passingCandidate(1, 0.7),
		passingCandidate(2, 0.9),
		passingCandidate(3, 0.8),
	))
	require.NoError(t, err)
	assert.True(t, decision.Accepted)
	assert.Equal(t, 2, decision.SelectedRound)
}

func TestGateNoCandidates(t *testing.T) {
	_, err := EvaluateGate(GateInput{Gate: strictGate()})
	require.Error(t, err)
}

func caseSnapshot(evalCaseID string, pass bool, score float64) CaseSnapshot {
	return CaseSnapshot{
		EvalSetID:  "validation",
		EvalCaseID: evalCaseID,
		Pass:       pass,
		Score:      score,
	}
}

func TestComputeDeltasKinds(t *testing.T) {
	baseline := []CaseSnapshot{
		caseSnapshot("a_new_pass", false, 0.0),
		caseSnapshot("b_new_fail", true, 1.0),
		caseSnapshot("c_improved", false, 0.2),
		caseSnapshot("d_regressed", true, 1.0),
		caseSnapshot("e_unchanged", true, 1.0),
	}
	candidate := []CaseSnapshot{
		caseSnapshot("a_new_pass", true, 1.0),
		caseSnapshot("b_new_fail", false, 0.5),
		caseSnapshot("c_improved", false, 0.6),
		caseSnapshot("d_regressed", true, 0.8),
		caseSnapshot("e_unchanged", true, 1.0),
	}
	deltas, err := ComputeDeltas(baseline, candidate, 0)
	require.NoError(t, err)
	kinds := make(map[string]DeltaKind, len(deltas))
	for _, delta := range deltas {
		kinds[delta.EvalCaseID] = delta.Kind
	}
	assert.Equal(t, DeltaNewPass, kinds["a_new_pass"])
	assert.Equal(t, DeltaNewFail, kinds["b_new_fail"])
	assert.Equal(t, DeltaImproved, kinds["c_improved"])
	assert.Equal(t, DeltaRegressed, kinds["d_regressed"])
	assert.Equal(t, DeltaUnchanged, kinds["e_unchanged"])

	summary := Summarize(deltas)
	assert.Equal(t, DeltaSummary{NewPass: 1, NewFail: 1, Improved: 1, Regressed: 1, Unchanged: 1}, summary)
}

// TestComputeDeltasPassFlipBeatsScore: a case that flips pass state is a
// flip even when the score moves the other way or not at all.
func TestComputeDeltasPassFlipBeatsScore(t *testing.T) {
	baseline := []CaseSnapshot{caseSnapshot("flip", true, 0.5)}
	candidate := []CaseSnapshot{caseSnapshot("flip", false, 0.9)}
	deltas, err := ComputeDeltas(baseline, candidate, 0)
	require.NoError(t, err)
	assert.Equal(t, DeltaNewFail, deltas[0].Kind)
	assert.True(t, deltas[0].Regressed())
}

func TestComputeDeltasEpsilon(t *testing.T) {
	baseline := []CaseSnapshot{caseSnapshot("eps", true, 0.5)}
	candidate := []CaseSnapshot{caseSnapshot("eps", true, 0.5000004)}
	deltas, err := ComputeDeltas(baseline, candidate, 1e-6)
	require.NoError(t, err)
	assert.Equal(t, DeltaUnchanged, deltas[0].Kind)

	// A larger epsilon absorbs bigger movements.
	candidate[0].Score = 0.509
	deltas, err = ComputeDeltas(baseline, candidate, 0.01)
	require.NoError(t, err)
	assert.Equal(t, DeltaUnchanged, deltas[0].Kind)

	deltas, err = ComputeDeltas(baseline, candidate, 1e-6)
	require.NoError(t, err)
	assert.Equal(t, DeltaImproved, deltas[0].Kind)
}

func TestComputeDeltasMisalignedCases(t *testing.T) {
	baseline := []CaseSnapshot{caseSnapshot("a", true, 1.0)}
	_, err := ComputeDeltas(baseline, nil, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing case")

	candidate := []CaseSnapshot{caseSnapshot("a", true, 1.0), caseSnapshot("b", true, 1.0)}
	_, err = ComputeDeltas(baseline, candidate, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown case")

	duplicated := []CaseSnapshot{caseSnapshot("a", true, 1.0), caseSnapshot("a", false, 0.0)}
	_, err = ComputeDeltas(baseline, duplicated, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
