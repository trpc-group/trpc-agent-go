//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"
)

// GateCheck records one gate condition outcome.
type GateCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// GateDecision is the overall acceptance decision produced by the gate.
type GateDecision struct {
	Accepted bool
	Checks   []GateCheck
	Reason   string
}

// EvaluateGate applies the configurable acceptance gate to a candidate against
// an accepted baseline. Every configured check must pass for the candidate to be
// accepted. The gate intentionally operates on the normalized EvalResult types so
// it can be unit tested in isolation from the PromptIter engine.
func EvaluateGate(
	cfg GateConfig,
	baseline *EvalResult,
	candidate *EvalResult,
	deltas []CaseDelta,
	modelCalls int,
	latencyMs int64,
) *GateDecision {
	checks := make([]GateCheck, 0, 4)
	// Check 1: the candidate must improve the validation score beyond the threshold.
	baselineScore := 0.0
	candidateScore := 0.0
	if baseline != nil {
		baselineScore = baseline.OverallScore
	}
	if candidate != nil {
		candidateScore = candidate.OverallScore
	}
	gain := candidateScore - baselineScore
	scoreCheck := GateCheck{
		Name: "validation_score_gain",
		Passed: gain >= cfg.MinScoreGain,
		Detail: fmt.Sprintf("validation %.3f -> %.3f (delta %+.3f), threshold %+.3f", baselineScore, candidateScore, gain, cfg.MinScoreGain),
	}
	checks = append(checks, scoreCheck)

	// Check 2: no more than MaxNewHardFails cases may turn from passing to failing.
	newHardFails := 0
	var newHardFailIDs []string
	for _, delta := range deltas {
		if delta.Outcome == DeltaNewlyFailed {
			newHardFails++
			newHardFailIDs = append(newHardFailIDs, delta.EvalCaseID)
		}
	}
	hardFailCheck := GateCheck{
		Name:   "no_new_hard_fail",
		Passed: newHardFails <= cfg.MaxNewHardFails,
	}
	if newHardFails == 0 {
		hardFailCheck.Detail = "0 new hard fails"
	} else {
		hardFailCheck.Detail = fmt.Sprintf("%d new hard fails (%s), limit %d", newHardFails, strings.Join(newHardFailIDs, ", "), cfg.MaxNewHardFails)
	}
	checks = append(checks, hardFailCheck)

	// Check 3: every key validation case must not regress below its baseline score.
	var regressedKeys []string
	for _, keyCaseID := range cfg.KeyCaseIDs {
		baselineCase := baseline.caseByID(keyCaseID)
		candidateCase := candidate.caseByID(keyCaseID)
		if baselineCase == nil || candidateCase == nil {
			continue
		}
		if candidateCase.Score < baselineCase.Score {
			regressedKeys = append(regressedKeys, keyCaseID)
		}
	}
	keyCheck := GateCheck{
		Name: "key_cases_no_regression",
		Passed: len(regressedKeys) == 0,
	}
	if len(regressedKeys) == 0 {
		keyCheck.Detail = fmt.Sprintf("%d key case(s) checked, no regression", len(cfg.KeyCaseIDs))
	} else {
		keyCheck.Detail = fmt.Sprintf("key case(s) regressed: %s", strings.Join(regressedKeys, ", "))
	}
	checks = append(checks, keyCheck)

	// Check 4: the run must stay within the model-call and latency budget.
	budgetOK := true
	if cfg.MaxModelCalls > 0 && modelCalls > cfg.MaxModelCalls {
		budgetOK = false
	}
	if cfg.MaxLatencyMs > 0 && latencyMs > cfg.MaxLatencyMs {
		budgetOK = false
	}
	budgetCheck := GateCheck{
		Name:   "budget_within_limit",
		Passed: budgetOK,
		Detail: fmt.Sprintf("%d model calls / %d ms", modelCalls, latencyMs),
	}
	checks = append(checks, budgetCheck)

	accepted := true
	var failedNames []string
	for _, check := range checks {
		if !check.Passed {
			accepted = false
			failedNames = append(failedNames, check.Name)
		}
	}
	decision := &GateDecision{
		Accepted: accepted,
		Checks:   checks,
	}
	if accepted {
		decision.Reason = "all gate checks passed"
	} else {
		decision.Reason = fmt.Sprintf("gate rejected candidate: %s", strings.Join(failedNames, ", "))
	}
	return decision
}
