//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateGateAcceptsCleanValidationGain(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MinValidationScoreGain: 0.1, RequireEngineAccepted: true, MaxModelCalls: 10},
		true,
		DeltaReport{OverallScoreDelta: 0.2},
		CostSummary{ModelCalls: 5, Source: CostSourceProvider},
		Duration{},
	)
	assert.True(t, decision.Accepted)
}

func TestEvaluateGateRejectsOverfitValidationRegression(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MinValidationScoreGain: 0.05, RequireEngineAccepted: true},
		true,
		DeltaReport{
			OverallScoreDelta: -0.1,
			Summary:           DeltaSummary{NewlyFailed: 1},
		},
		CostSummary{},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.True(t, containsSubstring(decision.Reasons, "validation score gain"))
	assert.True(t, containsSubstring(decision.Reasons, "newly failed hard"))
}

func TestEvaluateGateOnlyBlocksConfiguredHardFailMetrics(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{
			MinValidationScoreGain: 0.1,
			HardFailMetricNames:    []string{"final_response"},
		},
		true,
		DeltaReport{
			OverallScoreDelta: 0.2,
			Summary:           DeltaSummary{NewlyFailed: 1},
			Cases: []CaseDelta{
				{EvalCaseID: "soft_case", MetricName: "rubric", Kind: DeltaNewlyFailed},
			},
		},
		CostSummary{},
		Duration{},
	)
	assert.True(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "no newly failed hard validation metrics; 1 non-hard validation metrics newly failed")

	decision = EvaluateGate(
		GateConfig{
			MinValidationScoreGain: 0.1,
			HardFailMetricNames:    []string{"final_response"},
		},
		true,
		DeltaReport{
			OverallScoreDelta: 0.2,
			Summary:           DeltaSummary{NewlyFailed: 1},
			Cases: []CaseDelta{
				{EvalCaseID: "hard_case", MetricName: "final_response", Kind: DeltaNewlyFailed},
			},
		},
		CostSummary{},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "1 newly failed hard validation metrics: [hard_case/final_response]")
}

func TestEvaluateGateRejectsMissingCandidateMetricEvenWhenNotConfiguredHard(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{
			MinValidationScoreGain: 0.1,
			HardFailMetricNames:    []string{"final_response"},
		},
		true,
		DeltaReport{
			OverallScoreDelta: 0.2,
			Summary:           DeltaSummary{NewlyFailed: 1},
			Cases: []CaseDelta{
				{
					EvalCaseID:      "required_case",
					MetricName:      "rubric",
					BaselineStatus:  "failed",
					CandidateStatus: statusAbsent,
					Kind:            DeltaNewlyFailed,
				},
			},
		},
		CostSummary{},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "candidate validation missing baseline metrics: [required_case/rubric]")
	assert.Contains(t, decision.Reasons, "no newly failed hard validation metrics; 1 non-hard validation metrics newly failed")
}

func TestEvaluateGateRejectsCriticalCaseScoreDown(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MinValidationScoreGain: 0, CriticalCaseIDs: []string{"must_keep"}},
		true,
		DeltaReport{
			Cases: []CaseDelta{{EvalCaseID: "must_keep", Kind: DeltaScoreDown, Critical: true}},
		},
		CostSummary{},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "critical cases regressed: [must_keep]")
}

func TestEvaluateGateOptionallyRejectsAnyScoreDown(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MinValidationScoreGain: 0.1, RejectAnyScoreDown: true},
		true,
		DeltaReport{
			OverallScoreDelta: 0.2,
			Summary:           DeltaSummary{ScoreDown: 1},
			Cases: []CaseDelta{
				{EvalCaseID: "soft_regression", MetricName: "rubric", Kind: DeltaScoreDown},
			},
		},
		CostSummary{},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "score-down validation metrics: [soft_regression/rubric]")

	decision = EvaluateGate(
		GateConfig{MinValidationScoreGain: 0.1, RejectAnyScoreDown: true},
		true,
		DeltaReport{OverallScoreDelta: 0.2},
		CostSummary{},
		Duration{},
	)
	assert.True(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "no score-down validation metrics")
}

func TestEvaluateGateReportsCriticalCasesCleanAndDeduplicatesRegressions(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{CriticalCaseIDs: []string{"must_keep"}},
		true,
		DeltaReport{
			Cases: []CaseDelta{{EvalCaseID: "must_keep", Kind: DeltaUnchanged, Critical: true}},
		},
		CostSummary{},
		Duration{},
	)
	assert.True(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "critical cases did not regress")

	regressed := criticalRegressions(DeltaReport{Cases: []CaseDelta{
		{EvalCaseID: "must_keep", Kind: DeltaNewlyFailed, Critical: true},
		{EvalCaseID: "must_keep", Kind: DeltaScoreDown, Critical: true},
		{EvalCaseID: "ignored", Kind: DeltaNewlyFailed},
		{EvalCaseID: "not_regressed", Kind: DeltaUnchanged, Critical: true},
	}})
	assert.Equal(t, []string{"must_keep"}, regressed)
}

func TestEvaluateGateRejectsBudgets(t *testing.T) {
	maxLatency := Duration{Duration: time.Second}
	decision := EvaluateGate(
		GateConfig{MaxModelCalls: 2, MaxCost: 0.01, MaxLatency: &maxLatency},
		true,
		DeltaReport{},
		CostSummary{ModelCalls: 3, Amount: 0.02, AmountMeasured: true, Source: CostSourceProvider},
		Duration{Duration: 2 * time.Second},
	)
	assert.False(t, decision.Accepted)
}

func TestEvaluateGateRejectsMaxModelCallsWithoutMeasuredProviderCount(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MaxModelCalls: 10},
		true,
		DeltaReport{},
		CostSummary{ModelCalls: 5, Estimated: true, Source: CostSourceModelCallEstimate},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "model call count unavailable; configure CostProvider to enforce maxModelCalls")

	decision = EvaluateGate(
		GateConfig{MaxModelCalls: 10},
		true,
		DeltaReport{},
		CostSummary{ModelCalls: 5, Estimated: true, Source: CostSourceProvider},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "model call count unavailable; configure CostProvider to enforce maxModelCalls")
}

func TestEvaluateGateRejectsMaxCostWithoutMeasuredAmount(t *testing.T) {
	decision := EvaluateGate(
		GateConfig{MaxCost: 1},
		true,
		DeltaReport{},
		CostSummary{ModelCalls: 3, Estimated: true, Source: CostSourceModelCallEstimate},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "cost amount unavailable; configure CostProvider to enforce maxCost")

	decision = EvaluateGate(
		GateConfig{MaxCost: 1},
		true,
		DeltaReport{},
		CostSummary{ModelCalls: 3, Source: CostSourceProvider},
		Duration{},
	)
	assert.False(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "cost amount unavailable; configure CostProvider to enforce maxCost")
}

func TestEvaluateGateAllowsConfiguredNewFailuresAndBudgetsWithinLimit(t *testing.T) {
	maxLatency := Duration{Duration: time.Second}
	decision := EvaluateGate(
		GateConfig{
			MinValidationScoreGain: 0.1,
			AllowNewHardFail:       true,
			MaxModelCalls:          5,
			MaxCost:                1,
			MaxLatency:             &maxLatency,
		},
		false,
		DeltaReport{
			OverallScoreDelta: 0.2,
			Summary:           DeltaSummary{NewlyFailed: 1},
		},
		CostSummary{ModelCalls: 5, Amount: 0.5, AmountMeasured: true, Source: CostSourceProvider},
		Duration{Duration: 500 * time.Millisecond},
	)
	assert.True(t, decision.Accepted)
	assert.Contains(t, decision.Reasons, "1 newly failed hard validation metrics allowed by policy: [unknown]")
}

func containsSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}
