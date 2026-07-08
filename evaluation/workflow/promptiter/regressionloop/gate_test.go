//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateGateAcceptsCleanImprovement(t *testing.T) {
	decision := EvaluateGate(
		GatePolicy{MinValidationScoreGain: 0.05, AllowNewHardFails: false, BlockCriticalRegression: true, MaxCalls: 10, MaxLatencyMS: 100},
		evalSummary(0.7),
		evalSummary(0.8),
		nil,
		CostSummary{Calls: 2, EstimatedCost: 0.01},
		LatencySummary{TotalMS: 20},
	)
	assert.True(t, decision.Accepted)
	assert.Empty(t, decision.FailedRules)
}

func TestEvaluateGateRejectsRegressionAndBudgets(t *testing.T) {
	decision := EvaluateGate(
		GatePolicy{MinValidationScoreGain: 0.2, AllowNewHardFails: false, BlockCriticalRegression: true, MaxCost: 0.01, MaxCalls: 1, MaxLatencyMS: 10},
		evalSummary(0.7),
		evalSummary(0.75),
		[]CaseDelta{{EvalID: "critical", NewHardFail: true, CriticalRegression: true}},
		CostSummary{Calls: 2, EstimatedCost: 0.02},
		LatencySummary{TotalMS: 20},
	)
	assert.False(t, decision.Accepted)
	assert.ElementsMatch(t,
		[]string{"validation_score_gain", "no_new_hard_fails", "critical_case_non_regression", "max_cost", "max_calls", "max_latency"},
		decision.FailedRules,
	)
}
