//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// EvaluateGate evaluates every configured check in a stable order and never
// lets training gains compensate for validation regressions.
func EvaluateGate(policy GatePolicy, input GateInput) (GateDecision, error) {
	if err := validateGatePolicy(policy); err != nil {
		return GateDecision{}, err
	}
	if input.Delta == nil || input.BaselineValidation == nil || input.CandidateValidation == nil {
		return GateDecision{}, errors.New("delta, baseline validation, and candidate validation are required")
	}
	if err := validateSummaryForGate(input.BaselineValidation); err != nil {
		return GateDecision{}, fmt.Errorf("baseline validation: %w", err)
	}
	if err := validateSummaryForGate(input.CandidateValidation); err != nil {
		return GateDecision{}, fmt.Errorf("candidate validation: %w", err)
	}
	if err := validateUsage(input.BaselineUsage); err != nil {
		return GateDecision{}, fmt.Errorf("baseline usage: %w", err)
	}
	if err := validateUsage(input.CandidateUsage); err != nil {
		return GateDecision{}, fmt.Errorf("candidate usage: %w", err)
	}
	if !usageCovers(input.BaselineUsage, input.BaselineValidation.Usage) {
		return GateDecision{}, errors.New("baseline gate usage does not cover validation usage")
	}
	if !usageCovers(input.CandidateUsage, input.CandidateValidation.Usage) {
		return GateDecision{}, errors.New("candidate gate usage does not cover validation usage")
	}
	computedDelta, err := ComputeDelta(input.BaselineValidation, input.CandidateValidation)
	if err != nil {
		return GateDecision{}, fmt.Errorf("recompute validation delta: %w", err)
	}
	deltaConsistent := reflect.DeepEqual(input.Delta, computedDelta)
	delta := computedDelta
	decision := GateDecision{Accepted: true, Checks: make([]GateCheck, 0, 10), Reasons: make([]string, 0)}
	appendCheck := func(check GateCheck) {
		decision.Checks = append(decision.Checks, check)
		if !check.Passed {
			decision.Accepted = false
			decision.Reasons = append(decision.Reasons, check.Reason)
		}
	}

	appendCheck(GateCheck{
		Name:       "evaluation_comparable",
		Passed:     delta.Complete,
		Actual:     boolFloat(delta.Complete),
		Comparator: "==",
		Limit:      1,
		Reason:     comparableReason(delta),
	})
	appendCheck(GateCheck{
		Name:       "delta_consistent",
		Passed:     deltaConsistent,
		Actual:     boolFloat(deltaConsistent),
		Comparator: "==",
		Limit:      1,
		Reason:     chooseReason(deltaConsistent, "provided delta matches evaluation summaries", "provided delta does not match recomputed evaluation summaries"),
	})
	promptChanged := input.BaselinePromptHash != "" && input.CandidatePromptHash != "" &&
		input.BaselinePromptHash != input.CandidatePromptHash
	appendCheck(GateCheck{
		Name:       "prompt_changed",
		Passed:     promptChanged,
		Actual:     boolFloat(promptChanged),
		Comparator: "==",
		Limit:      1,
		Reason:     chooseReason(promptChanged, "candidate prompt changed", "candidate prompt is unchanged or its hash is missing"),
	})
	gainPassed := delta.ScoreDelta+scoreEpsilon >= policy.MinValidationScoreGain
	appendCheck(GateCheck{
		Name:       "min_validation_score_gain",
		Passed:     gainPassed,
		Actual:     delta.ScoreDelta,
		Comparator: ">=",
		Limit:      policy.MinValidationScoreGain,
		Reason: chooseReason(
			gainPassed,
			fmt.Sprintf("validation score gain %.6f meets threshold", delta.ScoreDelta),
			fmt.Sprintf("validation score gain %.6f is below %.6f", delta.ScoreDelta, policy.MinValidationScoreGain),
		),
	})
	if policy.MaxNewFailures != nil {
		passed := delta.NewFailures <= *policy.MaxNewFailures
		appendCheck(GateCheck{
			Name:       "max_new_failures",
			Passed:     passed,
			Actual:     float64(delta.NewFailures),
			Comparator: "<=",
			Limit:      float64(*policy.MaxNewFailures),
			Reason: chooseReason(
				passed,
				fmt.Sprintf("new failures %d are within limit", delta.NewFailures),
				fmt.Sprintf("new failures %d exceed limit %d", delta.NewFailures, *policy.MaxNewFailures),
			),
		})
	}
	if policy.RejectNewHardFails {
		passed := delta.NewHardFails == 0
		appendCheck(GateCheck{
			Name:       "no_new_hard_failures",
			Passed:     passed,
			Actual:     float64(delta.NewHardFails),
			Comparator: "==",
			Limit:      0,
			Reason: chooseReason(
				passed,
				"candidate introduced no hard failures",
				fmt.Sprintf("candidate introduced %d hard failures", delta.NewHardFails),
			),
		})
	}
	criticalRegressions, unknownCritical, worstCriticalDrop := criticalRegressionDetails(policy, delta)
	criticalPassed := len(criticalRegressions) == 0 && len(unknownCritical) == 0
	criticalReason := "critical cases did not regress"
	if !criticalPassed {
		criticalReason = fmt.Sprintf(
			"critical-case gate failed; regressed=%v unknown=%v",
			criticalRegressions,
			unknownCritical,
		)
	}
	appendCheck(GateCheck{
		Name:       "critical_cases_non_regression",
		Passed:     criticalPassed,
		Actual:     worstCriticalDrop,
		Comparator: "<=",
		Limit:      policy.MaxCriticalScoreDrop,
		Reason:     criticalReason,
	})
	if policy.MaxPerCaseScoreDrop != nil {
		worstDrop, caseIDs := worstCaseDrop(delta.Cases)
		passed := worstDrop <= *policy.MaxPerCaseScoreDrop+scoreEpsilon
		reason := fmt.Sprintf("maximum per-case score drop %.6f is within limit", worstDrop)
		if !passed {
			reason = fmt.Sprintf("maximum per-case score drop %.6f exceeds %.6f for %v", worstDrop, *policy.MaxPerCaseScoreDrop, caseIDs)
		}
		appendCheck(GateCheck{
			Name:       "max_per_case_score_drop",
			Passed:     passed,
			Actual:     worstDrop,
			Comparator: "<=",
			Limit:      *policy.MaxPerCaseScoreDrop,
			Reason:     reason,
		})
	}
	if policy.MaxCostUSD != nil {
		passed := input.CandidateUsage.CostUSD <= *policy.MaxCostUSD+scoreEpsilon
		appendCheck(numericBudgetCheck(
			"max_cost_usd",
			input.CandidateUsage.CostUSD,
			*policy.MaxCostUSD,
			passed,
			"candidate cost",
		))
	}
	if policy.MaxCostIncreaseRatio != nil {
		ratio, measurable := costIncreaseRatio(input.BaselineUsage.CostUSD, input.CandidateUsage.CostUSD)
		passed := measurable && ratio <= *policy.MaxCostIncreaseRatio+scoreEpsilon
		reason := "cost increase ratio is within limit"
		if !measurable {
			reason = "cost increase ratio is not measurable because baseline cost is zero"
		} else if !passed {
			reason = fmt.Sprintf("cost increase ratio %.6f exceeds %.6f", ratio, *policy.MaxCostIncreaseRatio)
		}
		appendCheck(GateCheck{
			Name:       "max_cost_increase_ratio",
			Passed:     passed,
			Actual:     ratio,
			Comparator: "<=",
			Limit:      *policy.MaxCostIncreaseRatio,
			Reason:     reason,
		})
	}
	if policy.MaxModelCalls != nil {
		passed := input.CandidateUsage.ModelCalls <= *policy.MaxModelCalls
		appendCheck(numericBudgetCheck(
			"max_model_calls",
			float64(input.CandidateUsage.ModelCalls),
			float64(*policy.MaxModelCalls),
			passed,
			"model calls",
		))
	}
	if policy.MaxTotalCalls != nil {
		totalCalls, err := checkedAddInt(input.CandidateUsage.ModelCalls, input.CandidateUsage.ToolCalls)
		if err != nil {
			return GateDecision{}, fmt.Errorf("candidate total calls: %w", err)
		}
		passed := totalCalls <= *policy.MaxTotalCalls
		appendCheck(numericBudgetCheck(
			"max_total_calls",
			float64(totalCalls),
			float64(*policy.MaxTotalCalls),
			passed,
			"model and tool calls",
		))
	}
	if policy.MaxLatencyMS != nil {
		passed := input.CandidateUsage.LatencyMS <= *policy.MaxLatencyMS
		appendCheck(numericBudgetCheck(
			"max_latency_ms",
			float64(input.CandidateUsage.LatencyMS),
			float64(*policy.MaxLatencyMS),
			passed,
			"candidate latency",
		))
	}
	if decision.Accepted {
		decision.Reasons = append(decision.Reasons, "all acceptance checks passed")
	}
	return decision, nil
}

func usageCovers(total, subset Usage) bool {
	return total.ModelCalls >= subset.ModelCalls &&
		total.ToolCalls >= subset.ToolCalls &&
		total.InputTokens >= subset.InputTokens &&
		total.OutputTokens >= subset.OutputTokens &&
		total.CostUSD+scoreEpsilon >= subset.CostUSD &&
		total.LatencyMS >= subset.LatencyMS
}

func validateSummaryForGate(summary *EvaluationSummary) error {
	if summary == nil {
		return errors.New("summary is nil")
	}
	if summary.EvalSetID == "" {
		return errors.New("eval set id is empty")
	}
	if len(summary.Cases) == 0 {
		return errors.New("evaluation has no cases")
	}
	if !validUnitScore(summary.OverallScore) {
		return errors.New("overall score must be finite and in [0,1]")
	}
	if !validUnitScore(summary.PassThreshold) {
		return errors.New("pass threshold must be finite and in [0,1]")
	}
	if _, err := indexCases(summary.Cases); err != nil {
		return err
	}
	total := 0.0
	for _, evalCase := range summary.Cases {
		if err := validateCaseScores(evalCase, summary.PassThreshold); err != nil {
			return fmt.Errorf("case %q: %w", evalCase.CaseID, err)
		}
		total += evalCase.Score
	}
	macroAverage := total / float64(len(summary.Cases))
	if math.Abs(macroAverage-summary.OverallScore) > 1e-6 {
		return fmt.Errorf("overall score %.9f does not match case macro average %.9f", summary.OverallScore, macroAverage)
	}
	return nil
}

func validateGatePolicy(policy GatePolicy) error {
	if !finiteScore(policy.MinValidationScoreGain) || policy.MinValidationScoreGain < 0 {
		return errors.New("min validation score gain must be finite and non-negative")
	}
	if !finiteScore(policy.MaxCriticalScoreDrop) || policy.MaxCriticalScoreDrop < 0 {
		return errors.New("max critical score drop must be finite and non-negative")
	}
	if policy.MaxNewFailures != nil && *policy.MaxNewFailures < 0 {
		return errors.New("max new failures cannot be negative")
	}
	if policy.MaxPerCaseScoreDrop != nil && (!finiteScore(*policy.MaxPerCaseScoreDrop) || *policy.MaxPerCaseScoreDrop < 0) {
		return errors.New("max per-case score drop must be finite and non-negative")
	}
	if policy.MaxCostUSD != nil && (!finiteScore(*policy.MaxCostUSD) || *policy.MaxCostUSD < 0) {
		return errors.New("max cost must be finite and non-negative")
	}
	if policy.MaxCostIncreaseRatio != nil && (!finiteScore(*policy.MaxCostIncreaseRatio) || *policy.MaxCostIncreaseRatio < 0) {
		return errors.New("max cost increase ratio must be finite and non-negative")
	}
	if policy.MaxModelCalls != nil && *policy.MaxModelCalls < 0 {
		return errors.New("max model calls cannot be negative")
	}
	if policy.MaxTotalCalls != nil && *policy.MaxTotalCalls < 0 {
		return errors.New("max total calls cannot be negative")
	}
	if policy.MaxLatencyMS != nil && *policy.MaxLatencyMS < 0 {
		return errors.New("max latency cannot be negative")
	}
	return nil
}

func comparableReason(delta *DeltaSummary) string {
	if delta.Complete {
		return "baseline and candidate cover the same cases and metrics"
	}
	return fmt.Sprintf("evaluation coverage is incomplete: %v", delta.CoverageIssues)
}

func criticalRegressionDetails(policy GatePolicy, delta *DeltaSummary) ([]string, []string, float64) {
	configured := make(map[string]struct{}, len(policy.CriticalCaseIDs))
	for _, id := range policy.CriticalCaseIDs {
		configured[id] = struct{}{}
	}
	found := make(map[string]struct{}, len(configured))
	regressed := make([]string, 0)
	worstDrop := 0.0
	for _, caseDelta := range delta.Cases {
		_, explicitlyCritical := configured[caseDelta.CaseID]
		if !caseDelta.Critical && !explicitlyCritical {
			continue
		}
		if explicitlyCritical {
			found[caseDelta.CaseID] = struct{}{}
		}
		drop := math.Max(0, -caseDelta.ScoreDelta)
		if drop > worstDrop {
			worstDrop = drop
		}
		if !caseDelta.CandidatePresent || caseDelta.BecameFailed || caseDelta.NewHardFail ||
			drop > policy.MaxCriticalScoreDrop+scoreEpsilon {
			regressed = append(regressed, caseDelta.CaseID)
		}
	}
	unknown := make([]string, 0)
	for id := range configured {
		if _, ok := found[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(regressed)
	sort.Strings(unknown)
	return regressed, unknown, worstDrop
}

func worstCaseDrop(cases []CaseDelta) (float64, []string) {
	worst := 0.0
	ids := make([]string, 0)
	for _, caseDelta := range cases {
		drop := math.Max(0, -caseDelta.ScoreDelta)
		switch {
		case drop > worst+scoreEpsilon:
			worst = drop
			ids = []string{caseDelta.CaseID}
		case math.Abs(drop-worst) <= scoreEpsilon && drop > scoreEpsilon:
			ids = append(ids, caseDelta.CaseID)
		}
	}
	sort.Strings(ids)
	return worst, ids
}

func numericBudgetCheck(name string, actual, limit float64, passed bool, label string) GateCheck {
	reason := fmt.Sprintf("%s %.6f is within budget", label, actual)
	if !passed {
		reason = fmt.Sprintf("%s %.6f exceeds %.6f", label, actual, limit)
	}
	return GateCheck{
		Name:       name,
		Passed:     passed,
		Actual:     actual,
		Comparator: "<=",
		Limit:      limit,
		Reason:     reason,
	}
}

func costIncreaseRatio(baseline, candidate float64) (float64, bool) {
	if baseline == 0 {
		if candidate == 0 {
			return 0, true
		}
		return math.MaxFloat64, false
	}
	return (candidate - baseline) / baseline, true
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func chooseReason(condition bool, success, failure string) string {
	if condition {
		return success
	}
	return failure
}
