//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"slices"
	"testing"
)

func TestDecideReleaseGate(t *testing.T) {
	policy := GatePolicy{
		MinValidationScoreGain: 0.05, MaxNewHardFailures: 0, RejectValidationRegression: true,
		RequireCompleteEvaluation: true, MaxLatencyIncrease: 1, MaxModelCallIncrease: 2,
		MaxToolCallIncrease: 2, MaxCostIncrease: 0.1,
	}
	tests := []struct {
		name   string
		input  GateInput
		reason string
	}{
		{"accept", validGateInput(), "all_release_gate_checks_passed"},
		{"hard failure", withGateInput(func(input *GateInput) { input.ValidationDelta.NewlyFailed = []string{"case"} }), "new_hard_failure"},
		{"overfit", withGateInput(func(input *GateInput) { input.CandidateValidationScore = 0.4 }), "overfitting"},
		{"no generalization", withGateInput(func(input *GateInput) { input.CandidateValidationScore = 0.5 }), "no_generalization"},
		{"incomplete", withGateInput(func(input *GateInput) { input.ActualValidationCases = 2 }), "evaluation_incomplete"},
		{"latency", withGateInput(func(input *GateInput) { input.CandidateLatencySeconds = 2 }), "latency_budget_exceeded"},
		{"model calls", withGateInput(func(input *GateInput) { input.CandidateUsage.ModelCalls = 4 }), "model_call_budget_exceeded"},
		{"tool calls", withGateInput(func(input *GateInput) { input.CandidateUsage.ToolCalls = 4 }), "tool_call_budget_exceeded"},
		{"cost", withGateInput(func(input *GateInput) { input.CandidateCost = 0.2 }), "cost_budget_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Decide(policy, test.input)
			if !slices.Contains(got.Reasons, test.reason) {
				t.Fatalf("reasons = %#v, want %q", got.Reasons, test.reason)
			}
		})
	}
}

func TestDecideCriticalCasesAndAllReasons(t *testing.T) {
	policy := GatePolicy{
		MinValidationScoreGain: 0.05, RejectValidationRegression: true, RequireCompleteEvaluation: true,
		CriticalCases: []CriticalCasePolicy{{CaseID: "must", MustPass: true}, {CaseID: "drop", MaxScoreDrop: 0.1}},
	}
	input := validGateInput()
	input.CandidateValidationScore = 0.3
	input.CandidateTrainScore = 0.9
	input.ValidationDelta = Delta{NewlyFailed: []string{"must"}, PerCase: []CaseDelta{
		{CaseID: "must", BaselinePass: true, CandidatePass: false, ScoreDelta: -1},
		{CaseID: "drop", BaselinePass: true, CandidatePass: true, ScoreDelta: -0.2},
	}}
	got := Decide(policy, input)
	for _, reason := range []string{"min_validation_gain_not_met", "new_hard_failure", "overfitting", "validation_regression", "critical_case_regression:must", "critical_case_regression:drop"} {
		if !slices.Contains(got.Reasons, reason) {
			t.Errorf("missing %q in %#v", reason, got.Reasons)
		}
	}
}

func TestModelCallBudgetRejectsCandidate(t *testing.T) {
	input := validGateInput()
	input.CandidateUsage.ModelCalls = input.InputUsage.ModelCalls + 2
	decision := Decide(GatePolicy{MaxModelCallIncrease: 1, MaxToolCallIncrease: 100, MaxLatencyIncrease: 100, MaxCostIncrease: 100}, input)
	assertGateReason(t, decision, "model_call_budget_exceeded")
}

func TestLatencyBudgetRejectsCandidate(t *testing.T) {
	input := validGateInput()
	input.CandidateLatencySeconds = input.InputLatencySeconds + 2
	decision := Decide(GatePolicy{MaxModelCallIncrease: 100, MaxToolCallIncrease: 100, MaxLatencyIncrease: 1, MaxCostIncrease: 100}, input)
	assertGateReason(t, decision, "latency_budget_exceeded")
}

func TestCostBudgetRejectsCandidate(t *testing.T) {
	input := validGateInput()
	input.CandidateCost = input.InputCost + 0.2
	decision := Decide(GatePolicy{MaxModelCallIncrease: 100, MaxToolCallIncrease: 100, MaxLatencyIncrease: 100, MaxCostIncrease: 0.1}, input)
	assertGateReason(t, decision, "cost_budget_exceeded")
}

func assertGateReason(t *testing.T, decision GateDecision, expected string) {
	t.Helper()
	if !slices.Contains(decision.Reasons, expected) {
		t.Fatalf("Gate reasons %v do not contain %q", decision.Reasons, expected)
	}
}

func validGateInput() GateInput {
	return GateInput{
		InputTrainScore: 0.5, CandidateTrainScore: 0.6, InputValidationScore: 0.5, CandidateValidationScore: 0.6,
		ExpectedTrainCases: 3, ActualTrainCases: 3, ExpectedValidationCases: 3, ActualValidationCases: 3,
		TrainEvaluationComplete: true, ValidationEvaluationComplete: true,
	}
}

func withGateInput(change func(*GateInput)) GateInput {
	input := validGateInput()
	change(&input)
	return input
}
