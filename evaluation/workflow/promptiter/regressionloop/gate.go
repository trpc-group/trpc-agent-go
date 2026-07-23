//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import "fmt"

// EvaluateGate applies acceptance policy to validation scores, deltas, and budgets.
func EvaluateGate(policy GatePolicy, baseline, candidate EvaluationSummary, deltas []CaseDelta, cost CostSummary, latency LatencySummary) GateDecision {
	scoreDelta := candidate.Score - baseline.Score
	decision := GateDecision{ScoreDelta: scoreDelta}
	if scoreDelta >= policy.MinValidationScoreGain {
		decision.PassedRules = append(decision.PassedRules, "validation_score_gain")
	} else {
		decision.FailedRules = append(decision.FailedRules, "validation_score_gain")
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation score gain %.4f is below threshold %.4f",
			scoreDelta,
			policy.MinValidationScoreGain,
		))
	}
	missingCases := countMissingValidationCases(deltas)
	if missingCases == 0 {
		decision.PassedRules = append(decision.PassedRules, "validation_case_coverage")
	} else {
		decision.FailedRules = append(decision.FailedRules, "validation_case_coverage")
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"baseline and candidate validation sets differ by %d case(s)",
			missingCases,
		))
	}
	if !policy.AllowNewHardFails {
		newHardFails := countNewHardFails(deltas)
		if newHardFails == 0 {
			decision.PassedRules = append(decision.PassedRules, "no_new_hard_fails")
		} else {
			decision.FailedRules = append(decision.FailedRules, "no_new_hard_fails")
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("candidate introduced %d new hard fail(s)", newHardFails))
		}
	}
	if policy.BlockCriticalRegression {
		regressions := countCriticalRegressions(deltas)
		if regressions == 0 {
			decision.PassedRules = append(decision.PassedRules, "critical_case_non_regression")
		} else {
			decision.FailedRules = append(decision.FailedRules, "critical_case_non_regression")
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("candidate regressed %d critical case(s)", regressions))
		}
	}
	if policy.MaxCost > 0 {
		if cost.EstimatedCost <= policy.MaxCost {
			decision.PassedRules = append(decision.PassedRules, "max_cost")
		} else {
			decision.FailedRules = append(decision.FailedRules, "max_cost")
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("estimated cost %.4f exceeds budget %.4f", cost.EstimatedCost, policy.MaxCost))
		}
	}
	if policy.MaxCalls > 0 {
		if cost.Calls <= policy.MaxCalls {
			decision.PassedRules = append(decision.PassedRules, "max_calls")
		} else {
			decision.FailedRules = append(decision.FailedRules, "max_calls")
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("call count %d exceeds budget %d", cost.Calls, policy.MaxCalls))
		}
	}
	if policy.MaxLatencyMS > 0 {
		if latency.TotalMS <= policy.MaxLatencyMS {
			decision.PassedRules = append(decision.PassedRules, "max_latency")
		} else {
			decision.FailedRules = append(decision.FailedRules, "max_latency")
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("latency %dms exceeds budget %dms", latency.TotalMS, policy.MaxLatencyMS))
		}
	}
	decision.Accepted = len(decision.FailedRules) == 0
	if decision.Accepted {
		decision.Reasons = append(decision.Reasons, "candidate satisfies all acceptance gates")
	}
	return decision
}

func countNewHardFails(deltas []CaseDelta) int {
	count := 0
	for _, delta := range deltas {
		if delta.NewHardFail {
			count++
		}
	}
	return count
}

func countCriticalRegressions(deltas []CaseDelta) int {
	count := 0
	for _, delta := range deltas {
		if delta.CriticalRegression {
			count++
		}
	}
	return count
}

func countMissingValidationCases(deltas []CaseDelta) int {
	count := 0
	for _, delta := range deltas {
		if delta.Transition == TransitionMissing {
			count++
		}
	}
	return count
}
