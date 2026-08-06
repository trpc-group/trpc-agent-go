//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"
)

func evaluate(set evalSet, prompt string, metrics metricConfig) evaluationSummary {
	result := evaluationSummary{
		SetName: set.Name,
		Cases:   make([]caseResult, 0, len(set.Cases)),
		Cost: costSummary{
			Calls:         len(set.Cases),
			LatencyMillis: int64(len(set.Cases) * 5),
		},
	}
	normalizedPrompt := strings.ToLower(prompt)
	for _, evalCase := range set.Cases {
		caseResult, attribution := evaluateCase(evalCase, normalizedPrompt, metrics)
		result.Cases = append(result.Cases, caseResult)
		result.Cost.EstimatedTokens += estimateTokens(prompt, evalCase.Input, evalCase.Expected)
		if caseResult.Passed {
			result.Passed++
		} else {
			result.Failed++
			result.Attributions = append(result.Attributions, attribution)
		}
		result.Score += caseResult.Score
	}
	result.Score /= float64(len(set.Cases))
	return result
}

func evaluateCase(evalCase evalCase, normalizedPrompt string, metrics metricConfig) (caseResult, failureAttribution) {
	result := caseResult{
		ID: evalCase.ID, Passed: true, Score: metrics.PassScore, Hard: evalCase.Hard,
		Trace: []string{"input:" + evalCase.ID},
	}
	for _, required := range evalCase.Required {
		result.Trace = append(result.Trace, "require:"+required)
		if strings.Contains(normalizedPrompt, strings.ToLower(required)) {
			result.Trace = append(result.Trace, "matched:"+required)
			if strings.HasPrefix(required, "route=") {
				result.ToolTrajectory = append(result.ToolTrajectory, "route:"+strings.TrimPrefix(required, "route="))
			}
			continue
		}
		result.Trace = append(result.Trace, "missing:"+required)
		result.Passed = false
		result.Score = metrics.FailScore
		result.Category = evalCase.FailureCategory
		result.Reason = fmt.Sprintf("required signal %q is missing", required)
		return result, failureAttribution{
			CaseID: evalCase.ID, Category: result.Category, Reason: result.Reason, Signal: "prompt_surface",
		}
	}
	for _, forbidden := range evalCase.Forbidden {
		result.Trace = append(result.Trace, "forbid:"+forbidden)
		if !strings.Contains(normalizedPrompt, strings.ToLower(forbidden)) {
			result.Trace = append(result.Trace, "absent:"+forbidden)
			continue
		}
		result.Trace = append(result.Trace, "present:"+forbidden)
		result.Passed = false
		result.Score = metrics.FailScore
		result.Category = "overfit"
		result.Reason = fmt.Sprintf("forbidden training-only signal %q is present", forbidden)
		return result, failureAttribution{
			CaseID: evalCase.ID, Category: result.Category, Reason: result.Reason, Signal: "validation_guard",
		}
	}
	return result, failureAttribution{}
}

func estimateTokens(parts ...string) int {
	words := 0
	for _, part := range parts {
		words += len(strings.Fields(part))
	}
	if words == 0 {
		return 1
	}
	return words * 2
}

func mergeCost(values ...costSummary) costSummary {
	var result costSummary
	for _, value := range values {
		result.Calls += value.Calls
		result.EstimatedTokens += value.EstimatedTokens
		result.LatencyMillis += value.LatencyMillis
	}
	return result
}

func summarizeAttributions(values ...evaluationSummary) map[string]int {
	result := make(map[string]int)
	for _, value := range values {
		for _, attribution := range value.Attributions {
			result[attribution.Category]++
		}
	}
	return result
}
