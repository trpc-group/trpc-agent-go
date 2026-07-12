// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateAccept(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
		MaxRegressedCases:   2,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
		{EvalCaseID: "case2", DeltaType: DeltaScoreUp},
		{EvalCaseID: "case3", DeltaType: DeltaUnchanged},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.85, deltas, 0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
	assert.GreaterOrEqual(t, len(decision.AcceptanceReasons), 1)
}

func TestGateRejectGainThreshold(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.1,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaUnchanged},
	}

	decision := EvaluateGate(config, 0.7, 0.75, 0.7, 0.75, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "below threshold")
}

func TestGateRejectNewHardFail(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.0,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyFailed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.7, 0.6, 0.7, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "exceeds limit")
}

func TestGateRejectCriticalRegression(t *testing.T) {
	config := GateConfig{
		MinValidationGain: 0.0,
		AllowNewHardFail:  true,
		CriticalCaseIDs:   []string{"critical_case"},
	}

	deltas := []CaseDelta{
		{EvalCaseID: "critical_case", DeltaType: DeltaNewlyFailed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.7, 0.6, 0.7, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "critical case regression")
}

func TestGateRejectProtectedRegression(t *testing.T) {
	config := GateConfig{
		MinValidationGain: 0.0,
		AllowNewHardFail:  true,
		ProtectedCaseIDs:  []string{"protected_case"},
	}

	deltas := []CaseDelta{
		{EvalCaseID: "protected_case", DeltaType: DeltaScoreDown},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.7, 0.6, 0.7, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "protected case regression")
}

func TestGateRejectMaxRegressedCases(t *testing.T) {
	config := GateConfig{
		MinValidationGain: 0.0,
		AllowNewHardFail:  true,
		MaxRegressedCases: 1,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyFailed},
		{EvalCaseID: "case2", DeltaType: DeltaScoreDown},
		{EvalCaseID: "case3", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.7, 0.6, 0.7, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "regressed cases")
}

func TestGateOverfitDetectionTrainImprovedValDegraded(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.0,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyFailed},
	}

	decision := EvaluateGate(config, 0.6, 0.55, 0.6, 0.75, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "overfit detected")
}

func TestGateOverfitDetectionTrainMuchBetterThanVal(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.0,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaScoreUp},
		{EvalCaseID: "case2", DeltaType: DeltaScoreUp},
	}

	decision := EvaluateGate(config, 0.6, 0.62, 0.6, 0.8, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "overfit detected")
}

func TestGateOverfitDetectionNoOverfit(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
		{EvalCaseID: "case2", DeltaType: DeltaScoreUp},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.82, deltas, 0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
}

func TestGateRejectCostExceedsBudget(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
		MaxCost:             10.0,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 15.0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "cost")
	assert.Contains(t, decision.RejectionReasons[0], "exceeds")
}

func TestGateRejectCallsExceedsBudget(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
		MaxCalls:            50,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 0, 100, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "calls")
	assert.Contains(t, decision.RejectionReasons[0], "exceeds")
}

func TestGateRejectLatencyExceedsBudget(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
		MaxLatencyMS:        10000,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 0, 0, 20000)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "latency")
	assert.Contains(t, decision.RejectionReasons[0], "exceeds")
}

func TestGateCostWithinBudget(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
		MaxCost:             20.0,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 15.0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
}

func TestGateMultipleRejectionReasons(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.1,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
		CriticalCaseIDs:     []string{"critical_case"},
	}

	deltas := []CaseDelta{
		{EvalCaseID: "critical_case", DeltaType: DeltaNewlyFailed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyFailed},
	}

	decision := EvaluateGate(config, 0.6, 0.65, 0.6, 0.65, deltas, 0, 0, 0)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.GreaterOrEqual(t, len(decision.RejectionReasons), 2)
}

func TestEngineGatePass(t *testing.T) {
	policy := AcceptancePolicy{MinScoreGain: 0.05}
	decision := EvaluateEngineGate(policy, 0.7, 0.8)
	assert.Equal(t, GateResultAccept, decision.Result)
	assert.Contains(t, decision.RuleResults[0].Reason, "passed")
}

func TestEngineGateFail(t *testing.T) {
	policy := AcceptancePolicy{MinScoreGain: 0.1}
	decision := EvaluateEngineGate(policy, 0.7, 0.75)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RuleResults[0].Reason, "failed")
}

func TestGateEmptyDeltas(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, []CaseDelta{}, 0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
}

func TestGateNoCriticalCasesConfigured(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    false,
		MaxNewHardFailCount: 0,
		CriticalCaseIDs:     []string{},
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
}

func TestGateAllowNewHardFail(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.05,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 2,
		MaxRegressedCases:   10,
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyFailed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyPassed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, 0.6, 0.8, deltas, 0, 0, 0)
	assert.Equal(t, GateResultAccept, decision.Result)
}

func TestCheckOverfitDetection(t *testing.T) {
	testCases := []struct {
		name        string
		trainDelta  float64
		valDelta    float64
		expected    bool
		containsStr string
	}{
		{"train improved val degraded", 0.1, -0.05, false, "overfit"},
		{"train improved val barely improved", 0.15, 0.005, false, "overfit"},
		{"train much better than val", 0.2, 0.03, false, "overfit"},
		{"both improved", 0.1, 0.12, true, "no overfit"},
		{"val improved more", 0.05, 0.1, true, "no overfit"},
		{"no improvement", 0, 0, true, "no overfit"},
		{"train improved val unchanged - no division by zero", 0.1, 0, false, "overfit"},
		{"train unchanged val unchanged", 0, 0, true, "no overfit"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			passed, reason := checkOverfitDetection(tc.trainDelta, tc.valDelta)
			assert.Equal(t, tc.expected, passed)
			assert.Contains(t, reason, tc.containsStr)
		})
	}
}

func TestCheckCostBudget(t *testing.T) {
	passed, _ := checkCostBudget(10.0, 20.0)
	assert.True(t, passed)
	passed, _ = checkCostBudget(25.0, 20.0)
	assert.False(t, passed)
}

func TestCheckCallsBudget(t *testing.T) {
	passed, _ := checkCallsBudget(40, 50)
	assert.True(t, passed)
	passed, _ = checkCallsBudget(60, 50)
	assert.False(t, passed)
}

func TestCheckLatencyBudget(t *testing.T) {
	passed, _ := checkLatencyBudget(5000, 10000)
	assert.True(t, passed)
	passed, _ = checkLatencyBudget(15000, 10000)
	assert.False(t, passed)
}
