//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"fmt"
	"strings"
)

// GateConfig configures the acceptance gate for the regression loop.
type GateConfig struct {
	// MinScoreGain is the minimum overall score improvement required.
	MinScoreGain float64

	// NoNewHardFailures rejects the candidate if any case that passed
	// in the baseline now fails in the candidate.
	NoNewHardFailures bool

	// CriticalCaseIDs lists case IDs that must not regress in score.
	CriticalCaseIDs []string

	// MaxCostBudget is the maximum allowed total token cost for the
	// candidate run. 0 means no budget check.
	MaxCostBudget float64

	// OverfitThreshold is the minimum train/val divergence that triggers
	// overfitting detection. If train score improves by more than this
	// threshold while validation score decreases, overfitting is flagged.
	OverfitThreshold float64
}

// DefaultGateConfig returns a GateConfig with sensible defaults.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		MinScoreGain:      0.01,
		NoNewHardFailures: true,
		OverfitThreshold:  0.05,
	}
}

// EvalRunSummary holds the aggregate results of one evaluation run,
// used for gate evaluation.
type EvalRunSummary struct {
	// OverallScore is the aggregate score across all cases and metrics.
	OverallScore float64

	// CaseScores maps evalSetID/evalCaseID to per-case average score.
	CaseScores map[string]float64

	// CaseStatuses maps evalSetID/evalCaseID to pass/fail status.
	CaseStatuses map[string]string

	// TotalTokens is the total token usage for this run.
	TotalTokens int

	// TotalCost is the estimated total cost for this run.
	TotalCost float64

	// CaseCount is the total number of evaluated cases.
	CaseCount int

	// PassCount is the number of cases that passed.
	PassCount int

	// FailCount is the number of cases that failed.
	FailCount int
}

// EvaluateGate runs all configured gate rules and produces a decision.
func EvaluateGate(
	cfg GateConfig,
	baseline, candidate EvalRunSummary,
	trainBaseline, trainCandidate *EvalRunSummary,
) *GateDecision {
	decision := &GateDecision{}

	// Rule 1: Minimum score gain.
	scoreDelta := candidate.OverallScore - baseline.OverallScore
	scoreRule := GateRuleResult{
		Rule:   "min_score_gain",
		Passed: scoreDelta >= cfg.MinScoreGain,
		Detail: fmt.Sprintf("score delta %.4f (threshold %.4f)", scoreDelta, cfg.MinScoreGain),
	}
	decision.Reasons = append(decision.Reasons, scoreRule)

	// Rule 2: No new hard failures.
	if cfg.NoNewHardFailures {
		newFailures := findNewFailures(baseline, candidate)
		noNewFailRule := GateRuleResult{
			Rule:   "no_new_hard_failures",
			Passed: len(newFailures) == 0,
			Detail: fmt.Sprintf("%d new failure(s)", len(newFailures)),
		}
		if len(newFailures) > 0 {
			noNewFailRule.Detail = fmt.Sprintf("new failures in: %s", strings.Join(newFailures, ", "))
		}
		decision.Reasons = append(decision.Reasons, noNewFailRule)
	}

	// Rule 3: Critical cases must not regress.
	if len(cfg.CriticalCaseIDs) > 0 {
		regressed := findRegressedCases(baseline, candidate, cfg.CriticalCaseIDs)
		criticalRule := GateRuleResult{
			Rule:   "critical_cases_no_regression",
			Passed: len(regressed) == 0,
			Detail: fmt.Sprintf("%d critical case(s) regressed", len(regressed)),
		}
		if len(regressed) > 0 {
			criticalRule.Detail = fmt.Sprintf("regressed critical cases: %s", strings.Join(regressed, ", "))
		}
		decision.Reasons = append(decision.Reasons, criticalRule)
	}

	// Rule 4: Cost budget.
	if cfg.MaxCostBudget > 0 {
		costRule := GateRuleResult{
			Rule:   "cost_budget",
			Passed: candidate.TotalCost <= cfg.MaxCostBudget,
			Detail: fmt.Sprintf("cost %.4f (budget %.4f)", candidate.TotalCost, cfg.MaxCostBudget),
		}
		decision.Reasons = append(decision.Reasons, costRule)
	}

	// Rule 5: Overfitting detection.
	if trainBaseline != nil && trainCandidate != nil {
		trainDelta := trainCandidate.OverallScore - trainBaseline.OverallScore
		valDelta := candidate.OverallScore - baseline.OverallScore
		overfit := trainDelta > cfg.OverfitThreshold && valDelta < 0

		if overfit {
			decision.OverfittingDetected = true
			overfitRule := GateRuleResult{
				Rule:   "no_overfitting",
				Passed: false,
				Detail: fmt.Sprintf("train improved by %.4f but validation dropped by %.4f (threshold %.4f)",
					trainDelta, -valDelta, cfg.OverfitThreshold),
			}
			decision.Reasons = append(decision.Reasons, overfitRule)
		} else {
			decision.Reasons = append(decision.Reasons, GateRuleResult{
				Rule:   "no_overfitting",
				Passed: true,
				Detail: fmt.Sprintf("train delta %.4f, val delta %.4f", trainDelta, valDelta),
			})
		}
	}

	// Aggregate decision.
	decision.Accepted = true
	var failedRules []string
	for _, r := range decision.Reasons {
		if !r.Passed {
			decision.Accepted = false
			failedRules = append(failedRules, r.Rule)
		}
	}

	if decision.Accepted {
		decision.Summary = fmt.Sprintf("accepted: score improved by %.4f", scoreDelta)
	} else {
		decision.Summary = fmt.Sprintf("rejected: failed rules [%s]", strings.Join(failedRules, ", "))
	}

	return decision
}

// ComputeCaseDeltas compares baseline and candidate results per-case.
func ComputeCaseDeltas(baseline, candidate EvalRunSummary) []CaseDelta {
	var deltas []CaseDelta

	// Collect all case keys from both.
	allKeys := make(map[string]struct{})
	for k := range baseline.CaseScores {
		allKeys[k] = struct{}{}
	}
	for k := range candidate.CaseScores {
		allKeys[k] = struct{}{}
	}

	for key := range allKeys {
		bScore := baseline.CaseScores[key]
		cScore := candidate.CaseScores[key]
		bStatus := baseline.CaseStatuses[key]
		cStatus := candidate.CaseStatuses[key]

		d := CaseDelta{
			EvalCaseID:      key,
			BaselineScore:   bScore,
			CandidateScore:  cScore,
			ScoreDelta:      cScore - bScore,
			BaselineStatus:  bStatus,
			CandidateStatus: cStatus,
		}

		bPassed := bStatus == "passed"
		cPassed := cStatus == "passed"

		if !bPassed && cPassed {
			d.IsNewPass = true
		}
		if bPassed && !cPassed {
			d.IsNewFailure = true
		}
		if !bPassed && !cPassed && d.ScoreDelta < 0 {
			d.IsRegression = true
		}
		if bPassed && cPassed && d.ScoreDelta > 0 {
			d.IsImprovement = true
		}

		deltas = append(deltas, d)
	}
	return deltas
}

// findNewFailures returns case IDs that passed in baseline but fail in candidate.
func findNewFailures(baseline, candidate EvalRunSummary) []string {
	var newFailures []string
	for key, bStatus := range baseline.CaseStatuses {
		cStatus, ok := candidate.CaseStatuses[key]
		if !ok {
			continue
		}
		if bStatus == "passed" && cStatus != "passed" {
			newFailures = append(newFailures, key)
		}
	}
	return newFailures
}

// findRegressedCases returns case IDs from the critical set whose scores decreased.
func findRegressedCases(baseline, candidate EvalRunSummary, criticalIDs []string) []string {
	var regressed []string
	for _, id := range criticalIDs {
		bScore, bOK := baseline.CaseScores[id]
		cScore, cOK := candidate.CaseScores[id]
		if bOK && cOK && cScore < bScore {
			regressed = append(regressed, id)
		}
	}
	return regressed
}
