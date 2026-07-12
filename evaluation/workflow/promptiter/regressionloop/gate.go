// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"fmt"
)

func EvaluateGate(config GateConfig, baselineScore, candidateScore float64, deltas []CaseDelta) GateDecision {
	ruleResults := []GateRuleResult{}
	rejectionReasons := []string{}
	acceptanceReasons := []string{}

	scoreDelta := candidateScore - baselineScore

	if config.MinValidationGain > 0 {
		passed, reason := checkValidationGainThreshold(scoreDelta, config.MinValidationGain)
		ruleResults = append(ruleResults, GateRuleResult{
			RuleType:    GateRuleValidationGainThreshold,
			Passed:      passed,
			Reason:      reason,
			Threshold:   config.MinValidationGain,
			ActualValue: scoreDelta,
		})
		if !passed {
			rejectionReasons = append(rejectionReasons, reason)
		} else {
			acceptanceReasons = append(acceptanceReasons, reason)
		}
	}

	if !config.AllowNewHardFail {
		newlyFailed := CountNewlyFailed(deltas)
		passed, reason := checkNewHardFailLimit(newlyFailed, config.MaxNewHardFailCount)
		ruleResults = append(ruleResults, GateRuleResult{
			RuleType:    GateRuleNewHardFailLimit,
			Passed:      passed,
			Reason:      reason,
			Threshold:   float64(config.MaxNewHardFailCount),
			ActualValue: float64(newlyFailed),
		})
		if !passed {
			rejectionReasons = append(rejectionReasons, reason)
		}
	}

	if len(config.CriticalCaseIDs) > 0 {
		criticalRegressed := countCriticalRegressions(deltas, config.CriticalCaseIDs)
		passed, reason := checkCriticalRegression(criticalRegressed)
		ruleResults = append(ruleResults, GateRuleResult{
			RuleType:    GateRuleCriticalRegressionDetection,
			Passed:      passed,
			Reason:      reason,
			Threshold:   0,
			ActualValue: float64(criticalRegressed),
		})
		if !passed {
			rejectionReasons = append(rejectionReasons, reason)
		}
	}

	if len(config.ProtectedCaseIDs) > 0 {
		protectedRegressed := countProtectedRegressions(deltas, config.ProtectedCaseIDs)
		passed, reason := checkProtectedCases(protectedRegressed)
		ruleResults = append(ruleResults, GateRuleResult{
			RuleType:    GateRuleProtectedCases,
			Passed:      passed,
			Reason:      reason,
			Threshold:   0,
			ActualValue: float64(protectedRegressed),
		})
		if !passed {
			rejectionReasons = append(rejectionReasons, reason)
		}
	}

	regressedCases := CountRegressedCases(deltas)
	if config.MaxRegressedCases >= 0 {
		passed, reason := checkMaxRegressedCases(regressedCases, config.MaxRegressedCases)
		ruleResults = append(ruleResults, GateRuleResult{
			RuleType:    GateRuleResourceBudget,
			Passed:      passed,
			Reason:      reason,
			Threshold:   float64(config.MaxRegressedCases),
			ActualValue: float64(regressedCases),
		})
		if !passed {
			rejectionReasons = append(rejectionReasons, reason)
		}
	}

	result := GateResultAccept
	if len(rejectionReasons) > 0 {
		result = GateResultReject
	}

	return GateDecision{
		Result:            result,
		Stage:             "security_gate",
		RuleResults:       ruleResults,
		RejectionReasons:  rejectionReasons,
		AcceptanceReasons: acceptanceReasons,
		ScoreDelta:        scoreDelta,
		BaselineScore:     baselineScore,
		CandidateScore:    candidateScore,
	}
}

func checkValidationGainThreshold(scoreDelta, minGain float64) (bool, string) {
	if scoreDelta >= minGain {
		return true, fmt.Sprintf("validation score gain %.4f meets threshold %.4f", scoreDelta, minGain)
	}
	return false, fmt.Sprintf("validation score gain %.4f below threshold %.4f", scoreDelta, minGain)
}

func checkNewHardFailLimit(newlyFailed, maxAllowed int) (bool, string) {
	if newlyFailed <= maxAllowed {
		return true, fmt.Sprintf("newly failed cases %d within limit %d", newlyFailed, maxAllowed)
	}
	return false, fmt.Sprintf("newly failed cases %d exceeds limit %d", newlyFailed, maxAllowed)
}

func countCriticalRegressions(deltas []CaseDelta, criticalCaseIDs []string) int {
	criticalSet := make(map[string]bool)
	for _, id := range criticalCaseIDs {
		criticalSet[id] = true
	}

	count := 0
	for _, delta := range deltas {
		if criticalSet[delta.EvalCaseID] && (delta.DeltaType == DeltaNewlyFailed || delta.DeltaType == DeltaScoreDown) {
			count++
		}
	}
	return count
}

func checkCriticalRegression(count int) (bool, string) {
	if count == 0 {
		return true, "no critical case regressions detected"
	}
	return false, fmt.Sprintf("detected %d critical case regression(s)", count)
}

func countProtectedRegressions(deltas []CaseDelta, protectedCaseIDs []string) int {
	protectedSet := make(map[string]bool)
	for _, id := range protectedCaseIDs {
		protectedSet[id] = true
	}

	count := 0
	for _, delta := range deltas {
		if protectedSet[delta.EvalCaseID] && (delta.DeltaType == DeltaNewlyFailed || delta.DeltaType == DeltaScoreDown) {
			count++
		}
	}
	return count
}

func checkProtectedCases(count int) (bool, string) {
	if count == 0 {
		return true, "no protected case regressions detected"
	}
	return false, fmt.Sprintf("detected %d protected case regression(s)", count)
}

func checkMaxRegressedCases(regressed, maxAllowed int) (bool, string) {
	if regressed <= maxAllowed {
		return true, fmt.Sprintf("regressed cases %d within limit %d", regressed, maxAllowed)
	}
	return false, fmt.Sprintf("regressed cases %d exceeds limit %d", regressed, maxAllowed)
}

func EvaluateEngineGate(policy AcceptancePolicy, baselineScore, candidateScore float64) GateDecision {
	scoreDelta := candidateScore - baselineScore
	accepted := scoreDelta >= policy.MinScoreGain

	var reason string
	if accepted {
		reason = fmt.Sprintf("engine gate passed: score gain %.4f >= threshold %.4f", scoreDelta, policy.MinScoreGain)
	} else {
		reason = fmt.Sprintf("engine gate failed: score gain %.4f < threshold %.4f", scoreDelta, policy.MinScoreGain)
	}

	result := GateResultReject
	if accepted {
		result = GateResultAccept
	}

	return GateDecision{
		Result:           result,
		Stage:            "engine_gate",
		RuleResults:      []GateRuleResult{{RuleType: GateRuleValidationGainThreshold, Passed: accepted, Reason: reason, Threshold: policy.MinScoreGain, ActualValue: scoreDelta}},
		ScoreDelta:       scoreDelta,
		BaselineScore:    baselineScore,
		CandidateScore:   candidateScore,
	}
}

type AcceptancePolicy struct {
	MinScoreGain float64
}