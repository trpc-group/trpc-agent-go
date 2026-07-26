//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"math"
	"strings"
)

// DecideRelease applies held-out quality, safety, and model-call threshold gates.
func DecideRelease(
	policy GatePolicy,
	delta DeltaSummary,
	cumulative ResourceUsage,
) Decision {
	scoreDelta := delta.ScoreDelta
	decision := Decision{
		Status:     DecisionRejected,
		ScoreDelta: &scoreDelta,
	}
	epsilon, policyErr := comparisonEpsilon(policy.Epsilon)
	validationErrors := make([]string, 0)
	if policyErr == nil {
		validationErrors = validateReleaseInputs(
			policy,
			delta,
			cumulative,
			epsilon,
		)
	}
	if policyErr != nil {
		validationErrors = append(validationErrors, policyErr.Error())
	}
	if len(validationErrors) > 0 {
		decision.Status = DecisionNotEvaluable
		decision.Reasons = validationErrors
		if !isFinite(delta.ScoreDelta) {
			decision.ScoreDelta = nil
		}
		return decision
	}

	reasons := make([]string, 0, 4)
	if delta.ScoreDelta < -epsilon {
		reasons = append(
			reasons,
			fmt.Sprintf(
				"validation_regression: oriented gain %.6g is below zero",
				delta.ScoreDelta,
			),
		)
	}
	if delta.ScoreDelta+epsilon < policy.MinValidationGain {
		reasons = append(
			reasons,
			fmt.Sprintf(
				"minimum_validation_gain: gain %.6g is below required %.6g",
				delta.ScoreDelta,
				policy.MinValidationGain,
			),
		)
	}
	for _, item := range delta.Cases {
		if policy.NoNewHardFailures &&
			item.HardFailure &&
			item.PrimaryKind == ChangeNewlyFailing {
			reasons = append(
				reasons,
				fmt.Sprintf(
					"new_hard_failure: case %s/%s changed from passed to failed",
					item.EvalSetID,
					item.CaseID,
				),
			)
		}
		if policy.NoCriticalRegressions &&
			item.Critical &&
			caseHasRegression(item, epsilon) {
			reasons = append(
				reasons,
				fmt.Sprintf(
					"critical_regression: case %s/%s regressed",
					item.EvalSetID,
					item.CaseID,
				),
			)
		}
	}
	if policy.ModelCallStopThreshold > 0 &&
		cumulative.ModelCalls.Value > policy.ModelCallStopThreshold {
		reasons = append(
			reasons,
			fmt.Sprintf(
				"model_call_threshold: observed %d calls; threshold is %d",
				cumulative.ModelCalls.Value,
				policy.ModelCallStopThreshold,
			),
		)
	}
	if len(reasons) > 0 {
		decision.Status = DecisionRejected
		decision.Reasons = reasons
		return decision
	}
	decision.Status = DecisionAccepted
	decision.Reasons = []string{"candidate satisfies every configured release gate"}
	return decision
}

func validateReleaseInputs(
	policy GatePolicy,
	delta DeltaSummary,
	cumulative ResourceUsage,
	epsilon float64,
) []string {
	reasons := make([]string, 0)
	policyValid := true
	if err := validateDeltaPolicy(policy); err != nil {
		reasons = append(reasons, "invalid_policy: "+err.Error())
		policyValid = false
	}
	if !isFinite(policy.MinValidationGain) || policy.MinValidationGain < 0 {
		reasons = append(
			reasons,
			"invalid_policy: minimum validation gain must be finite and non-negative",
		)
	}
	if policy.ModelCallStopThreshold < 0 {
		reasons = append(
			reasons,
			"invalid_policy: model-call stop threshold must be non-negative",
		)
	}
	if delta.Comparison != "vs_released" {
		reasons = append(
			reasons,
			fmt.Sprintf(
				"invalid_delta: release decision requires comparison %q, got %q",
				"vs_released",
				delta.Comparison,
			),
		)
	}
	if strings.TrimSpace(delta.BeforeProfileHash) == "" ||
		strings.TrimSpace(delta.AfterProfileHash) == "" {
		reasons = append(reasons, "invalid_delta: profile binding is incomplete")
	}
	if !isFinite(delta.BeforeOverallScore) ||
		!isFinite(delta.AfterOverallScore) ||
		!isFinite(delta.ScoreDelta) {
		reasons = append(reasons, "invalid_delta: scores must be finite")
	} else if policyValid {
		direction := policy.MetricDirections[policy.PrimaryMetric]
		expectedGain := orientedDelta(
			delta.AfterOverallScore-delta.BeforeOverallScore,
			direction,
		)
		if math.Abs(expectedGain-delta.ScoreDelta) > epsilon {
			reasons = append(
				reasons,
				fmt.Sprintf(
					"invalid_delta: oriented score delta %.17g does not match before/after gain %.17g",
					delta.ScoreDelta,
					expectedGain,
				),
			)
		}
	}
	if len(delta.Cases) == 0 {
		reasons = append(reasons, "invalid_delta: case deltas are empty")
	} else if policyValid {
		if err := validateDeltaSummary(delta, policy, epsilon); err != nil {
			reasons = append(reasons, "invalid_delta: "+err.Error())
		}
	}
	reasons = append(reasons, validateResourceUsage(cumulative)...)
	if policy.ModelCallStopThreshold > 0 &&
		!cumulative.ModelCalls.Available {
		reasons = append(
			reasons,
			"missing_measurement: cumulative model-call count is unavailable",
		)
	}
	return reasons
}

//nolint:gocyclo // Fail-closed delta validation enumerates independent safety invariants.
func validateDeltaSummary(
	delta DeltaSummary,
	policy GatePolicy,
	epsilon float64,
) error {
	counts := map[ChangeKind]int{
		ChangeNewlyPassing: 0,
		ChangeNewlyFailing: 0,
		ChangeImproved:     0,
		ChangeRegressed:    0,
		ChangeUnchanged:    0,
	}
	seen := make(map[snapshotCaseKey]struct{}, len(delta.Cases))
	var beforePrimaryTotal float64
	var afterPrimaryTotal float64
	for _, item := range delta.Cases {
		if strings.TrimSpace(item.EvalSetID) == "" ||
			strings.TrimSpace(item.CaseID) == "" {
			return errorsf("case identity is incomplete")
		}
		key := snapshotCaseKey{
			evalSetID: item.EvalSetID,
			caseID:    item.CaseID,
		}
		if _, ok := seen[key]; ok {
			return errorsf(
				"duplicate case delta %s/%s",
				item.EvalSetID,
				item.CaseID,
			)
		}
		seen[key] = struct{}{}
		if !isComparableResultStatus(item.BeforeStatus) ||
			!isComparableResultStatus(item.AfterStatus) ||
			!statusMatchesPassed(item.BeforeStatus, item.BeforePassed) ||
			!statusMatchesPassed(item.AfterStatus, item.AfterPassed) {
			return errorsf(
				"case %s/%s has invalid status binding",
				item.EvalSetID,
				item.CaseID,
			)
		}
		if len(item.Metrics) != len(policy.MetricDirections) {
			return errorsf(
				"case %s/%s metric inventory has %d entries, policy requires %d",
				item.EvalSetID,
				item.CaseID,
				len(item.Metrics),
				len(policy.MetricDirections),
			)
		}
		metricNames := make(map[string]struct{}, len(item.Metrics))
		primaryCount := 0
		var primary MetricDelta
		for _, metric := range item.Metrics {
			if strings.TrimSpace(metric.MetricName) == "" {
				return errorsf(
					"case %s/%s has an empty metric name",
					item.EvalSetID,
					item.CaseID,
				)
			}
			if _, ok := metricNames[metric.MetricName]; ok {
				return errorsf(
					"case %s/%s has duplicate metric delta %q",
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
				)
			}
			metricNames[metric.MetricName] = struct{}{}
			expectedDirection, configured := policy.MetricDirections[metric.MetricName]
			if !configured {
				return errorsf(
					"case %s/%s has metric %q outside policy inventory",
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
				)
			}
			if !isFinite(metric.BeforeScore) ||
				!isFinite(metric.AfterScore) ||
				!isFinite(metric.Delta) ||
				!isValidDirection(metric.Direction) ||
				!isComparableResultStatus(metric.BeforeStatus) ||
				!isComparableResultStatus(metric.AfterStatus) {
				return errorsf(
					"case %s/%s metric %q is not comparable",
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
				)
			}
			if metric.Direction != expectedDirection {
				return errorsf(
					"case %s/%s metric %q direction %q does not match policy %q",
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
					metric.Direction,
					expectedDirection,
				)
			}
			expectedDelta := metric.AfterScore - metric.BeforeScore
			if math.Abs(metric.Delta-expectedDelta) > epsilon {
				return errorsf(
					"case %s/%s metric %q delta %.17g does not match after-before %.17g",
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
					metric.Delta,
					expectedDelta,
				)
			}
			if metric.MetricName == policy.PrimaryMetric {
				primaryCount++
				primary = metric
			}
		}
		for metricName := range policy.MetricDirections {
			if _, ok := metricNames[metricName]; !ok {
				return errorsf(
					"case %s/%s is missing policy metric %q",
					item.EvalSetID,
					item.CaseID,
					metricName,
				)
			}
		}
		if primaryCount != 1 {
			return errorsf(
				"case %s/%s must contain exactly one primary metric %q, got %d",
				item.EvalSetID,
				item.CaseID,
				policy.PrimaryMetric,
				primaryCount,
			)
		}
		beforePrimaryTotal += primary.BeforeScore
		afterPrimaryTotal += primary.AfterScore

		expectedKind := recomputePrimaryKind(item, primary, epsilon)
		if item.PrimaryKind != expectedKind {
			return errorsf(
				"case %s/%s primary kind %q does not match recomputed kind %q",
				item.EvalSetID,
				item.CaseID,
				item.PrimaryKind,
				expectedKind,
			)
		}
		counts[expectedKind]++
	}
	caseCount := float64(len(delta.Cases))
	expectedBeforeOverall := beforePrimaryTotal / caseCount
	expectedAfterOverall := afterPrimaryTotal / caseCount
	if math.Abs(delta.BeforeOverallScore-expectedBeforeOverall) > epsilon {
		return errorsf(
			"before overall score %.17g does not match primary metric mean %.17g",
			delta.BeforeOverallScore,
			expectedBeforeOverall,
		)
	}
	if math.Abs(delta.AfterOverallScore-expectedAfterOverall) > epsilon {
		return errorsf(
			"after overall score %.17g does not match primary metric mean %.17g",
			delta.AfterOverallScore,
			expectedAfterOverall,
		)
	}
	if counts[ChangeNewlyPassing] != delta.NewlyPassing ||
		counts[ChangeNewlyFailing] != delta.NewlyFailing ||
		counts[ChangeImproved] != delta.Improved ||
		counts[ChangeRegressed] != delta.Regressed ||
		counts[ChangeUnchanged] != delta.Unchanged {
		return errorsf("aggregate change counts do not match case deltas")
	}
	return nil
}

func recomputePrimaryKind(
	item CaseDelta,
	primary MetricDelta,
	epsilon float64,
) ChangeKind {
	switch {
	case !item.BeforePassed && item.AfterPassed:
		return ChangeNewlyPassing
	case item.BeforePassed && !item.AfterPassed:
		return ChangeNewlyFailing
	}
	gain := primary.AfterScore - primary.BeforeScore
	if primary.Direction == ScoreLowerIsBetter {
		gain = -gain
	}
	switch {
	case gain > epsilon:
		return ChangeImproved
	case gain < -epsilon:
		return ChangeRegressed
	default:
		return ChangeUnchanged
	}
}

func validateResourceUsage(usage ResourceUsage) []string {
	reasons := make([]string, 0)
	counts := []struct {
		name  string
		count Count
	}{
		{name: "model calls", count: usage.ModelCalls},
		{name: "input tokens", count: usage.InputTokens},
		{name: "output tokens", count: usage.OutputTokens},
		{name: "latency", count: usage.LatencyMS},
	}
	for _, item := range counts {
		if item.count.Available && item.count.Value < 0 {
			reasons = append(
				reasons,
				fmt.Sprintf(
					"invalid_measurement: %s must be non-negative",
					item.name,
				),
			)
		}
		if !item.count.Available && item.count.Value != 0 {
			reasons = append(
				reasons,
				fmt.Sprintf(
					"invalid_measurement: unavailable %s has a value",
					item.name,
				),
			)
		}
	}
	if usage.MonetaryCost.Available {
		if !isFinite(usage.MonetaryCost.Value) ||
			usage.MonetaryCost.Value < 0 {
			reasons = append(
				reasons,
				"invalid_measurement: monetary cost must be finite and non-negative",
			)
		}
	} else if usage.MonetaryCost.Value != 0 {
		reasons = append(
			reasons,
			"invalid_measurement: unavailable monetary cost has a value",
		)
	}
	return reasons
}

func caseHasRegression(item CaseDelta, epsilon float64) bool {
	if item.PrimaryKind == ChangeNewlyFailing ||
		item.PrimaryKind == ChangeRegressed {
		return true
	}
	for _, metric := range item.Metrics {
		if orientedDelta(metric.Delta, metric.Direction) < -epsilon {
			return true
		}
	}
	return false
}
