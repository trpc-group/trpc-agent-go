//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestPolicyDecideRejectsIncompleteInput(t *testing.T) {
	_, err := NewPolicy().Decide(nil)
	require.ErrorContains(t, err, "gate input is incomplete")
}

func TestPolicyDecideAppliesQualityAndMetricRules(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*regression.GateInput)
		decision  regression.Decision
		rule      string
	}{
		{name: "accepts complete evidence", decision: regression.DecisionAccepted},
		{
			name: "invalid candidate profile rejects",
			configure: func(input *regression.GateInput) {
				input.CandidateProfileValid = false
				input.CandidateProfileReason = "surface outside target"
			},
			decision: regression.DecisionRejected, rule: "target_surface_scope",
		},
		{
			name: "unchanged candidate profile rejects",
			configure: func(input *regression.GateInput) {
				input.CandidateProfileChanged = false
			},
			decision: regression.DecisionRejected, rule: "profile_changed",
		},
		{
			name:      "incomplete evidence is inconclusive",
			configure: func(input *regression.GateInput) { input.ValidationDelta.Complete = false },
			decision:  regression.DecisionInconclusive, rule: "complete_results",
		},
		{
			name: "new validation failure rejects",
			configure: func(input *regression.GateInput) {
				input.Spec.Gate.RejectAnyNewFail = true
				input.ValidationDelta.NewFailures = 1
			},
			decision: regression.DecisionRejected, rule: "new_failures",
		},
		{
			name: "metric regression rejects",
			configure: func(input *regression.GateInput) {
				input.Spec.Gate.MaxCaseRegression = .1
				input.ValidationDelta.Cases = []regression.CaseDelta{{Metrics: []regression.MetricDelta{{BaselineScore: 1, CandidateScore: .5}}}}
			},
			decision: regression.DecisionRejected, rule: "case_regression",
		},
		{
			name: "missing floor evidence is inconclusive",
			configure: func(input *regression.GateInput) {
				input.Spec.MetricPolicies["safety"] = regression.MetricPolicy{Floor: 1}
			},
			decision: regression.DecisionInconclusive, rule: "metric_floor/safety",
		},
		{
			name: "low floor rejects",
			configure: func(input *regression.GateInput) {
				input.Spec.MetricPolicies["quality"] = regression.MetricPolicy{Floor: .9}
				input.CandidateValidation.Cases[0].Metrics[0].Score = .8
			},
			decision: regression.DecisionRejected, rule: "metric_floor/quality",
		},
		{
			name: "overfitting rejects",
			configure: func(input *regression.GateInput) {
				input.Spec.Gate.MaxGeneralizationGap = .1
				input.TrainDelta.WeightedScoreDelta = .5
			},
			decision: regression.DecisionRejected, rule: "generalization_gap",
		},
		{
			name: "score variance rejects",
			configure: func(input *regression.GateInput) {
				input.Spec.Gate.MaxScoreStdDev = .1
				input.CandidateValidation.ScoreStdDev = .2
			},
			decision: regression.DecisionRejected, rule: "score_stability",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			if test.configure != nil {
				test.configure(input)
			}
			actual, err := NewPolicy().Decide(input)
			require.NoError(t, err)
			assert.Equal(t, test.decision, actual.Decision)
			if test.rule != "" {
				assert.False(t, rulePassed(actual, test.rule))
			}
		})
	}
}

func TestPolicyDecideAppliesBudgetRulesAndWarnings(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*regression.GateInput)
		decision  regression.Decision
		rule      string
	}{
		{
			name: "incomplete configured usage is inconclusive",
			configure: func(input *regression.GateInput) {
				input.Spec.Budget.MaxCalls = 1
				input.TotalUsage.Complete = false
			},
			decision: regression.DecisionInconclusive, rule: "usage_complete",
		},
		{
			name: "unknown required cost is inconclusive",
			configure: func(input *regression.GateInput) {
				input.Spec.Budget.RequireKnownCost = true
				input.TotalUsage.CostKnown = false
			},
			decision: regression.DecisionInconclusive, rule: "known_cost",
		},
		{
			name: "call token cost and time limits reject",
			configure: func(input *regression.GateInput) {
				input.Spec.Budget = regression.BudgetPolicy{MaxCalls: 1, MaxTokens: 2, MaxEstimatedCost: .1, MaxWallTime: time.Second}
				input.TotalUsage = regression.UsageSummary{Calls: 2, TotalTokens: 3, EstimatedCost: .2, CostKnown: true, Latency: 2 * time.Second, Complete: true}
			},
			decision: regression.DecisionRejected, rule: "call_budget",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			test.configure(input)
			actual, err := NewPolicy().Decide(input)
			require.NoError(t, err)
			assert.Equal(t, test.decision, actual.Decision)
			assert.False(t, rulePassed(actual, test.rule))
		})
	}

	input := validInput()
	input.PromptIterAccepted = false
	input.PromptIterReason = "exploration threshold not met"
	actual, err := NewPolicy().Decide(input)
	require.NoError(t, err)
	assert.Equal(t, regression.DecisionAccepted, actual.Decision)
	assert.Equal(t, []string{"exploration threshold not met"}, actual.Warnings)
}

func validInput() *regression.GateInput {
	return &regression.GateInput{
		Spec:                    &regression.RunSpec{MetricPolicies: map[string]regression.MetricPolicy{"quality": {}}},
		PromptIterAccepted:      true,
		CandidateProfileValid:   true,
		CandidateProfileChanged: true,
		CandidateValidation: &regression.EvaluationSnapshot{Complete: true, Cases: []regression.CaseResult{{
			Metrics: []regression.MetricResult{{Name: "quality", Score: 1}},
		}}},
		TrainDelta:      &regression.DeltaReport{Complete: true, WeightedScoreDelta: .2},
		ValidationDelta: &regression.DeltaReport{Complete: true, WeightedScoreDelta: .2},
		TotalUsage:      regression.UsageSummary{Complete: true, CostKnown: true},
	}
}

func rulePassed(decision *regression.GateDecision, name string) bool {
	for _, rule := range decision.Rules {
		if rule.Rule == name {
			return rule.Passed
		}
	}
	return true
}
