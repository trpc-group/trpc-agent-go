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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type measurement[T int | int64 | float64] struct {
	Known bool `json:"known"`
	Value T    `json:"value"`
}

func knownInt(value int) measurement[int] {
	return measurement[int]{Known: true, Value: value}
}

type usageSummary struct {
	ModelCalls    measurement[int]     `json:"modelCalls"`
	ToolCalls     measurement[int]     `json:"toolCalls"`
	Tokens        measurement[int]     `json:"tokens"`
	EstimatedCost measurement[float64] `json:"estimatedCost"`
	LatencyMillis measurement[int64]   `json:"latencyMillis"`
}

type gatePolicy struct {
	MinGain          float64
	MaxHardFailures  int
	MaxCaseScoreDrop float64
	MaxModelCalls    *int
	MaxToolCalls     *int
	MaxTokens        *int
	MaxEstimatedCost *float64
	MaxLatencyMillis *int64
	Critical         []criticalRule
}

type gateInput struct {
	Policy    gatePolicy
	Baseline  evaluationSnapshot
	Candidate evaluationSnapshot
	Usage     usageSummary
	RunError  string
}

type gateCheck struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Passed   bool   `json:"passed"`
	Observed any    `json:"observed,omitempty"`
	Limit    any    `json:"limit,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type gateDecision struct {
	Accepted bool        `json:"accepted"`
	Checks   []gateCheck `json:"checks"`
}

func decide(input gateInput) gateDecision {
	checks := make([]gateCheck, 0, 11)
	checks = append(checks, gateCheck{
		ID:       "run_status",
		Enabled:  true,
		Passed:   input.RunError == "",
		Observed: input.RunError,
		Reason:   failureReason(input.RunError != "", "run did not complete successfully"),
	})

	_, shapeErr := compareSnapshots(input.Baseline, input.Candidate)
	checks = append(checks, gateCheck{
		ID:       "evidence_shape",
		Enabled:  true,
		Passed:   shapeErr == nil,
		Observed: errorString(shapeErr),
		Reason:   failureReason(shapeErr != nil, "baseline and candidate evidence differ"),
	})
	gain := input.Candidate.Score - input.Baseline.Score
	checks = append(checks, gateCheck{
		ID:       "minimum_gain",
		Enabled:  true,
		Passed:   shapeErr == nil && gain >= input.Policy.MinGain,
		Observed: gain,
		Limit:    input.Policy.MinGain,
		Reason:   failureReason(shapeErr != nil || gain < input.Policy.MinGain, "validation gain is below the minimum"),
	})

	hardFailures := countHardFailures(input.Candidate)
	checks = append(checks, gateCheck{
		ID:       "hard_failures",
		Enabled:  true,
		Passed:   hardFailures <= input.Policy.MaxHardFailures,
		Observed: hardFailures,
		Limit:    input.Policy.MaxHardFailures,
		Reason:   failureReason(hardFailures > input.Policy.MaxHardFailures, "candidate retains too many hard failures"),
	})

	criticalOK, criticalReason := checkCriticalRules(input.Policy.Critical, input.Baseline, input.Candidate)
	checks = append(checks, gateCheck{
		ID:       "critical_case",
		Enabled:  len(input.Policy.Critical) != 0,
		Passed:   criticalOK,
		Observed: criticalReason,
		Reason:   failureReason(!criticalOK, "a critical rule failed"),
	})

	caseDropOK, largestDrop := checkCaseDrop(input.Baseline, input.Candidate, input.Policy.MaxCaseScoreDrop)
	checks = append(checks, gateCheck{
		ID:       "case_drop",
		Enabled:  true,
		Passed:   shapeErr == nil && caseDropOK,
		Observed: largestDrop,
		Limit:    input.Policy.MaxCaseScoreDrop,
		Reason:   failureReason(shapeErr != nil || !caseDropOK, "a case score drop exceeds the limit"),
	})

	checks = append(checks,
		intBudgetCheck("model_calls", input.Usage.ModelCalls, input.Policy.MaxModelCalls),
		intBudgetCheck("tool_calls", input.Usage.ToolCalls, input.Policy.MaxToolCalls),
		intBudgetCheck("tokens", input.Usage.Tokens, input.Policy.MaxTokens),
		floatBudgetCheck("estimated_cost", input.Usage.EstimatedCost, input.Policy.MaxEstimatedCost),
		int64BudgetCheck("latency", input.Usage.LatencyMillis, input.Policy.MaxLatencyMillis),
	)

	decision := gateDecision{Accepted: true, Checks: checks}
	for _, check := range checks {
		if !check.Passed {
			decision.Accepted = false
			break
		}
	}
	return decision
}

func countHardFailures(snapshot evaluationSnapshot) int {
	count := 0
	for _, evalCase := range snapshot.Cases {
		failed := evalCase.Status != status.EvalStatusPassed || evalCase.ExecutionError != ""
		for _, metric := range evalCase.Metrics {
			if metric.Status != status.EvalStatusPassed {
				failed = true
				break
			}
		}
		if failed {
			count++
		}
	}
	return count
}

func checkCriticalRules(rules []criticalRule, baseline, candidate evaluationSnapshot) (bool, string) {
	for _, rule := range rules {
		before, beforeOK := criticalScore(baseline, rule)
		after, afterOK := criticalScore(candidate, rule)
		if !beforeOK || !afterOK {
			return false, fmt.Sprintf("missing critical evidence for %s/%s", rule.EvalCaseID, rule.MetricName)
		}
		if rule.MustPass && !criticalPassed(candidate, rule) {
			return false, fmt.Sprintf("critical evidence %s/%s did not pass", rule.EvalCaseID, rule.MetricName)
		}
		if rule.MinScore != nil && after < *rule.MinScore {
			return false, fmt.Sprintf("critical evidence %s/%s score is below minimum", rule.EvalCaseID, rule.MetricName)
		}
		if rule.MaxScoreDrop != nil && before-after > *rule.MaxScoreDrop {
			return false, fmt.Sprintf("critical evidence %s/%s score drop exceeds maximum", rule.EvalCaseID, rule.MetricName)
		}
	}
	return true, ""
}

func criticalScore(snapshot evaluationSnapshot, rule criticalRule) (float64, bool) {
	for _, evalCase := range snapshot.Cases {
		if evalCase.EvalCaseID != rule.EvalCaseID {
			continue
		}
		if rule.MetricName == "" {
			return evalCase.Score, true
		}
		for _, metric := range evalCase.Metrics {
			if metric.Name == rule.MetricName {
				return metric.Score, true
			}
		}
	}
	return 0, false
}

func criticalPassed(snapshot evaluationSnapshot, rule criticalRule) bool {
	for _, evalCase := range snapshot.Cases {
		if evalCase.EvalCaseID != rule.EvalCaseID {
			continue
		}
		if rule.MetricName == "" {
			return evalCase.Status == status.EvalStatusPassed
		}
		for _, metric := range evalCase.Metrics {
			if metric.Name == rule.MetricName {
				return metric.Status == status.EvalStatusPassed
			}
		}
	}
	return false
}

func checkCaseDrop(baseline, candidate evaluationSnapshot, limit float64) (bool, float64) {
	candidateCases := make(map[string]caseResult, len(candidate.Cases))
	for _, evalCase := range candidate.Cases {
		candidateCases[evalCase.EvalCaseID] = evalCase
	}
	var largest float64
	for _, before := range baseline.Cases {
		after, ok := candidateCases[before.EvalCaseID]
		if !ok {
			return false, largest
		}
		drop := before.Score - after.Score
		if drop > largest {
			largest = drop
		}
	}
	return largest <= limit, largest
}

func intBudgetCheck(id string, observed measurement[int], limit *int) gateCheck {
	if limit == nil {
		return gateCheck{ID: id, Passed: true}
	}
	passed := observed.Known && observed.Value <= *limit
	return gateCheck{ID: id, Enabled: true, Passed: passed, Observed: observed, Limit: *limit,
		Reason: failureReason(!passed, "measurement is unavailable or exceeds the budget")}
}

func int64BudgetCheck(id string, observed measurement[int64], limit *int64) gateCheck {
	if limit == nil {
		return gateCheck{ID: id, Passed: true}
	}
	passed := observed.Known && observed.Value <= *limit
	return gateCheck{ID: id, Enabled: true, Passed: passed, Observed: observed, Limit: *limit,
		Reason: failureReason(!passed, "measurement is unavailable or exceeds the budget")}
}

func floatBudgetCheck(id string, observed measurement[float64], limit *float64) gateCheck {
	if limit == nil {
		return gateCheck{ID: id, Passed: true}
	}
	passed := observed.Known && observed.Value <= *limit
	return gateCheck{ID: id, Enabled: true, Passed: passed, Observed: observed, Limit: *limit,
		Reason: failureReason(!passed, "measurement is unavailable or exceeds the budget")}
}

func failureReason(failed bool, reason string) string {
	if failed {
		return reason
	}
	return ""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
