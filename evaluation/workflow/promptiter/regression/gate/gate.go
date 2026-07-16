//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package gate applies deterministic prompt-regression acceptance policy.
package gate

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// Policy evaluates quality, overfitting, safety, stability, and budget rules.
type Policy struct{}

// NewPolicy creates an acceptance policy.
func NewPolicy() *Policy {
	return &Policy{}
}

// Decide evaluates rules in a stable order and returns all decision evidence.
func (p *Policy) Decide(input *regression.GateInput) (*regression.GateDecision, error) {
	if !validGateInput(input) {
		return nil, errors.New("gate input is incomplete")
	}
	builder := decisionBuilder{
		decision: &regression.GateDecision{Decision: regression.DecisionAccepted},
	}
	builder.addQualityRules(input)
	builder.addMetricFloorRules(input)
	builder.addBudgetRules(input)
	return builder.decision, nil
}

func validGateInput(input *regression.GateInput) bool {
	return input != nil && input.Spec != nil && input.CandidateValidation != nil &&
		input.ValidationDelta != nil
}

type decisionBuilder struct {
	decision *regression.GateDecision
}

func (b *decisionBuilder) add(
	rule string,
	passed bool,
	observed any,
	threshold any,
	reason string,
	inconclusive bool,
) {
	ruleReason := ""
	if !passed {
		ruleReason = reason
	}
	b.decision.Rules = append(b.decision.Rules, regression.GateRuleResult{
		Rule: rule, Passed: passed, Observed: observed, Threshold: threshold, Reason: ruleReason,
	})
	if passed {
		return
	}
	b.decision.Reasons = append(b.decision.Reasons, reason)
	if inconclusive && b.decision.Decision == regression.DecisionAccepted {
		b.decision.Decision = regression.DecisionInconclusive
		return
	}
	if !inconclusive {
		b.decision.Decision = regression.DecisionRejected
	}
}

func (b *decisionBuilder) addQualityRules(input *regression.GateInput) {
	policy := input.Spec.Gate
	if policy.RequirePromptIterAcceptance {
		reason := input.PromptIterReason
		if reason == "" {
			reason = "PromptIter did not accept this round"
		}
		b.add("promptiter_acceptance", input.PromptIterAccepted,
			input.PromptIterAccepted, true, reason, false)
	} else if !input.PromptIterAccepted {
		reason := input.PromptIterReason
		if reason == "" {
			reason = "PromptIter did not accept this round"
		}
		b.decision.Warnings = append(b.decision.Warnings, reason)
	}
	b.add("target_surface_scope", input.CandidateProfileValid, input.CandidateProfileValid, true,
		input.CandidateProfileReason, false)
	b.add("profile_changed", input.CandidateProfileChanged, input.CandidateProfileChanged, true,
		"candidate does not change the configured target surface", false)
	complete := input.ValidationDelta.Complete
	if input.TrainDelta != nil {
		complete = complete && input.TrainDelta.Complete
	}
	// A release decision cannot treat missing cases or metrics as optional: doing
	// so would allow a candidate to improve its score by dropping bad evidence.
	b.add("complete_results", complete, complete, true,
		"evaluation results are incomplete", true)
	b.add("new_failures", !policy.RejectAnyNewFail || input.ValidationDelta.NewFailures == 0,
		input.ValidationDelta.NewFailures, 0, "validation introduced new failures", false)
	b.add("new_hard_failures", input.ValidationDelta.NewHardFailures == 0,
		input.ValidationDelta.NewHardFailures, 0, "validation introduced hard failures", false)
	b.add("critical_regressions", input.ValidationDelta.CriticalRegressions == 0,
		input.ValidationDelta.CriticalRegressions, 0, "critical validation cases regressed", false)
	worstRegression := worstMetricRegression(input.ValidationDelta)
	b.add("case_regression", worstRegression <= policy.MaxCaseRegression,
		worstRegression, policy.MaxCaseRegression, "a validation metric regressed beyond the allowed limit", false)
	b.add("validation_gain", input.ValidationDelta.WeightedScoreDelta >= policy.MinValidationGain,
		input.ValidationDelta.WeightedScoreDelta, policy.MinValidationGain,
		"validation gain is below the required minimum", false)
	if policy.MaxGeneralizationGap > 0 {
		trainAvailable := input.TrainDelta != nil
		b.add("train_delta_available", trainAvailable, trainAvailable, true,
			"candidate training delta is unavailable for the generalization gate", true)
	}
	if input.TrainDelta != nil && policy.MaxGeneralizationGap > 0 {
		generalizationGap := input.TrainDelta.WeightedScoreDelta - input.ValidationDelta.WeightedScoreDelta
		b.add("generalization_gap", generalizationGap <= policy.MaxGeneralizationGap,
			generalizationGap, policy.MaxGeneralizationGap,
			"train and validation gains indicate overfitting", false)
	}
	if policy.MaxScoreStdDev > 0 {
		b.add("score_stability", input.CandidateValidation.ScoreStdDev <= policy.MaxScoreStdDev,
			input.CandidateValidation.ScoreStdDev, policy.MaxScoreStdDev,
			"validation score variance exceeds the allowed limit", false)
	}
}

func (b *decisionBuilder) addMetricFloorRules(input *regression.GateInput) {
	names := sortedMetricNames(input.Spec.MetricPolicies)
	for _, name := range names {
		policy := input.Spec.MetricPolicies[name]
		if policy.Floor <= 0 {
			continue
		}
		minimum, found := minimumMetric(input.CandidateValidation, name)
		if !found {
			b.add("metric_floor/"+name, false, "missing", policy.Floor,
				fmt.Sprintf("metric %q is missing from validation evidence", name), true)
			continue
		}
		b.add("metric_floor/"+name, minimum >= policy.Floor, minimum, policy.Floor,
			fmt.Sprintf("metric %q is below its floor", name), false)
	}
}

func sortedMetricNames(policies map[string]regression.MetricPolicy) []string {
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (b *decisionBuilder) addBudgetRules(input *regression.GateInput) {
	budget := input.Spec.Budget
	usage := input.TotalUsage
	if budgetConfigured(budget) {
		b.add("usage_complete", usage.Complete, usage.Complete, true,
			"resource usage does not cover the complete optimization pipeline", true)
	}
	if budget.RequireKnownCost || budget.MaxEstimatedCost > 0 {
		b.add("known_cost", usage.CostKnown, usage.CostKnown, true,
			"estimated cost is unknown", true)
	}
	if budget.MaxCalls > 0 {
		b.add("call_budget", usage.Calls <= budget.MaxCalls,
			usage.Calls, budget.MaxCalls, "model call budget exceeded", false)
	}
	if budget.MaxTokens > 0 {
		b.add("token_budget", usage.TotalTokens <= budget.MaxTokens,
			usage.TotalTokens, budget.MaxTokens, "token budget exceeded", false)
	}
	b.addCostRule(budget, usage)
	if budget.MaxPromptIterLatency > 0 {
		b.add("promptiter_latency_budget", usage.PromptIterLatency <= budget.MaxPromptIterLatency,
			usage.PromptIterLatency, budget.MaxPromptIterLatency,
			"PromptIter latency budget exceeded", false)
	}
}

func budgetConfigured(budget regression.BudgetPolicy) bool {
	return budget.MaxCalls > 0 || budget.MaxTokens > 0 ||
		budget.MaxEstimatedCost > 0 || budget.MaxPromptIterLatency > 0 ||
		budget.RequireKnownCost
}

func (b *decisionBuilder) addCostRule(
	budget regression.BudgetPolicy,
	usage regression.UsageSummary,
) {
	if budget.MaxEstimatedCost <= 0 {
		return
	}
	if usage.CostKnown {
		b.add("cost_budget", usage.EstimatedCost <= budget.MaxEstimatedCost,
			usage.EstimatedCost, budget.MaxEstimatedCost,
			"estimated cost budget exceeded", false)
	}
}

func worstMetricRegression(report *regression.DeltaReport) float64 {
	worst := 0.0
	for _, caseDelta := range report.Cases {
		for _, metric := range caseDelta.Metrics {
			worst = math.Max(worst, metric.BaselineScore-metric.CandidateScore)
		}
	}
	return worst
}

func minimumMetric(snapshot *regression.EvaluationSnapshot, name string) (float64, bool) {
	minimum := math.Inf(1)
	found := false
	for _, caseResult := range snapshot.Cases {
		for _, metric := range caseResult.Metrics {
			if metric.Name != name {
				continue
			}
			minimum = math.Min(minimum, metric.Score)
			found = true
		}
	}
	return minimum, found
}
