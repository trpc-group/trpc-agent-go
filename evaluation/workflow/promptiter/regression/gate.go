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
	"fmt"
	"math"
)

// CriticalCasePolicy protects one validation case from regressions.
type CriticalCasePolicy struct {
	CaseID       string  `json:"caseId"`
	MustPass     bool    `json:"mustPass"`
	MaxScoreDrop float64 `json:"maxScoreDrop"`
}

// GatePolicy contains release thresholds and resource budgets.
type GatePolicy struct {
	MinValidationScoreGain     float64              `json:"minValidationScoreGain"`
	MaxNewHardFailures         int                  `json:"maxNewHardFailures"`
	RejectValidationRegression bool                 `json:"rejectValidationRegression"`
	CriticalCases              []CriticalCasePolicy `json:"criticalCases"`
	RequireCompleteEvaluation  bool                 `json:"requireCompleteEvaluation"`
	MaxLatencyIncrease         float64              `json:"maxLatencyIncreaseSeconds"`
	MaxModelCallIncrease       int                  `json:"maxModelCallIncrease"`
	MaxToolCallIncrease        int                  `json:"maxToolCallIncrease"`
	MaxCostIncrease            float64              `json:"maxCostIncrease"`
}

// GateInput contains same-baseline quality, completeness, and budget measurements.
type GateInput struct {
	InputTrainScore              float64
	CandidateTrainScore          float64
	InputValidationScore         float64
	CandidateValidationScore     float64
	ValidationDelta              Delta
	ExpectedTrainCases           int
	ActualTrainCases             int
	ExpectedValidationCases      int
	ActualValidationCases        int
	TrainEvaluationComplete      bool
	ValidationEvaluationComplete bool
	InputUsage                   Usage
	CandidateUsage               Usage
	InputLatencySeconds          float64
	CandidateLatencySeconds      float64
	InputCost                    float64
	CandidateCost                float64
}

// GateCheck records one named release check and its observed value.
type GateCheck struct {
	Passed   bool   `json:"passed"`
	Observed string `json:"observed"`
}

// GateDecision is the independent release decision. All failed checks are retained.
type GateDecision struct {
	Accepted bool                 `json:"accepted"`
	Reasons  []string             `json:"reasons"`
	Checks   map[string]GateCheck `json:"checks"`
}

// Decide evaluates every release check without short-circuiting.
func Decide(policy GatePolicy, input GateInput) GateDecision {
	const epsilon = 1e-9
	decision := GateDecision{Accepted: true, Reasons: []string{}, Checks: make(map[string]GateCheck)}
	add := func(name string, passed bool, observed, reason string) {
		decision.Checks[name] = GateCheck{Passed: passed, Observed: observed}
		if !passed {
			decision.Accepted = false
			decision.Reasons = append(decision.Reasons, reason)
		}
	}
	validationDelta := input.CandidateValidationScore - input.InputValidationScore
	trainDelta := input.CandidateTrainScore - input.InputTrainScore
	add("minValidationGain", validationDelta+epsilon >= policy.MinValidationScoreGain,
		fmt.Sprintf("validation delta %.4f, required %.4f", validationDelta, policy.MinValidationScoreGain), "min_validation_gain_not_met")
	add("newHardFailures", len(input.ValidationDelta.NewlyFailed) <= policy.MaxNewHardFailures,
		fmt.Sprintf("%d new failures, maximum %d", len(input.ValidationDelta.NewlyFailed), policy.MaxNewHardFailures), "new_hard_failure")
	add("overfitting", !(trainDelta > epsilon && validationDelta < -epsilon),
		fmt.Sprintf("train delta %.4f, validation delta %.4f", trainDelta, validationDelta), "overfitting")
	add("noGeneralization", !(trainDelta > epsilon && math.Abs(validationDelta) <= epsilon),
		fmt.Sprintf("train delta %.4f, validation delta %.4f", trainDelta, validationDelta), "no_generalization")
	if policy.RejectValidationRegression {
		add("validationRegression", validationDelta >= -epsilon,
			fmt.Sprintf("validation delta %.4f", validationDelta), "validation_regression")
	}
	caseIndex := make(map[string]CaseDelta, len(input.ValidationDelta.PerCase))
	for _, item := range input.ValidationDelta.PerCase {
		caseIndex[item.CaseID] = item
	}
	criticalOK := true
	for _, critical := range policy.CriticalCases {
		item, ok := caseIndex[critical.CaseID]
		failed := !ok || critical.MustPass && !item.CandidatePass || ok && item.ScoreDelta < -critical.MaxScoreDrop-epsilon
		if failed {
			criticalOK = false
			decision.Reasons = append(decision.Reasons, "critical_case_regression:"+critical.CaseID)
		}
	}
	decision.Checks["criticalCases"] = GateCheck{Passed: criticalOK, Observed: fmt.Sprintf("%d critical policies", len(policy.CriticalCases))}
	if !criticalOK {
		decision.Accepted = false
	}
	complete := !policy.RequireCompleteEvaluation || input.ExpectedTrainCases == input.ActualTrainCases && input.ExpectedValidationCases == input.ActualValidationCases && input.TrainEvaluationComplete && input.ValidationEvaluationComplete
	add("evaluationCompleteness", complete,
		fmt.Sprintf("train %d/%d complete=%t, validation %d/%d complete=%t", input.ActualTrainCases, input.ExpectedTrainCases, input.TrainEvaluationComplete, input.ActualValidationCases, input.ExpectedValidationCases, input.ValidationEvaluationComplete), "evaluation_incomplete")
	add("latencyBudget", input.CandidateLatencySeconds-input.InputLatencySeconds <= policy.MaxLatencyIncrease+epsilon,
		fmt.Sprintf("latency increase %.4fs, maximum %.4fs", input.CandidateLatencySeconds-input.InputLatencySeconds, policy.MaxLatencyIncrease), "latency_budget_exceeded")
	add("modelCallBudget", input.CandidateUsage.ModelCalls-input.InputUsage.ModelCalls <= policy.MaxModelCallIncrease,
		fmt.Sprintf("model call increase %d, maximum %d", input.CandidateUsage.ModelCalls-input.InputUsage.ModelCalls, policy.MaxModelCallIncrease), "model_call_budget_exceeded")
	add("toolCallBudget", input.CandidateUsage.ToolCalls-input.InputUsage.ToolCalls <= policy.MaxToolCallIncrease,
		fmt.Sprintf("tool call increase %d, maximum %d", input.CandidateUsage.ToolCalls-input.InputUsage.ToolCalls, policy.MaxToolCallIncrease), "tool_call_budget_exceeded")
	add("costBudget", input.CandidateCost-input.InputCost <= policy.MaxCostIncrease+epsilon,
		fmt.Sprintf("cost increase %.6f, maximum %.6f", input.CandidateCost-input.InputCost, policy.MaxCostIncrease), "cost_budget_exceeded")
	if decision.Accepted {
		decision.Reasons = append(decision.Reasons, "all_release_gate_checks_passed")
	}
	return decision
}
