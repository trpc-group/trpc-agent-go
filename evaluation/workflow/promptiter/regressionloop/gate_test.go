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

	decision := EvaluateGate(config, 0.6, 0.8, deltas)
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

	decision := EvaluateGate(config, 0.7, 0.75, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.7, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.7, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.7, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.7, deltas)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "regressed cases")
}

func TestGateOverfitDetection(t *testing.T) {
	config := GateConfig{
		MinValidationGain:   0.1,
		AllowNewHardFail:    true,
		MaxNewHardFailCount: 1,
		MaxRegressedCases:   10,
		CriticalCaseIDs:     []string{"val_02_protected"},
	}

	deltas := []CaseDelta{
		{EvalCaseID: "case1", DeltaType: DeltaNewlyPassed},
		{EvalCaseID: "case2", DeltaType: DeltaNewlyPassed},
		{EvalCaseID: "val_02_protected", DeltaType: DeltaNewlyFailed},
	}

	decision := EvaluateGate(config, 0.6, 0.8, deltas)
	assert.Equal(t, GateResultReject, decision.Result)
	assert.Contains(t, decision.RejectionReasons[0], "critical case regression")
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

	decision := EvaluateGate(config, 0.6, 0.65, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.8, []CaseDelta{})
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

	decision := EvaluateGate(config, 0.6, 0.8, deltas)
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

	decision := EvaluateGate(config, 0.6, 0.8, deltas)
	assert.Equal(t, GateResultAccept, decision.Result)
}
