//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Attribute classifies every failed metric in one evaluation result by likely
// root cause, returning per-category counts and per-failure detail. It reads
// only the deterministic signals available on the result (metric name, status,
// reason); tool/route categories additionally rely on the metric name.
func Attribute(result *engine.EvaluationResult) AttributionReport {
	counts := map[FailureCategory]int{}
	details := make([]FailureDetail, 0)
	if result == nil {
		return AttributionReport{Baseline: counts, BySeverity: map[string]int{}}
	}
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			for _, m := range evalCase.Metrics {
				if m.Status != status.EvalStatusFailed {
					continue
				}
				category := categorize(m.MetricName, m.Reason)
				counts[category]++
				details = append(details, FailureDetail{
					EvalSetID:  set.EvalSetID,
					EvalCaseID: evalCase.EvalCaseID,
					MetricName: m.MetricName,
					Category:   category,
					Reason:     m.Reason,
				})
			}
		}
	}
	return AttributionReport{
		Baseline:   counts,
		BySeverity: map[string]int{},
		Details:    details,
	}
}

// categorize maps one failed metric to a failure category using the metric name
// first (tool/response families) and then reason keywords.
func categorize(metricName, reason string) FailureCategory {
	name := strings.ToLower(metricName)
	why := strings.ToLower(reason)

	if strings.Contains(name, "tool_trajectory") || strings.Contains(name, "trajectory") {
		switch {
		case containsAny(why, "argument", "参数", "arg "):
			return CategoryToolArgError
		case containsAny(why, "route", "路由", "wrong agent", "transfer"):
			return CategoryRouteError
		default:
			return CategoryToolError
		}
	}
	if containsAny(why, "format", "格式", "markdown", "schema", "xml", "json") {
		return CategoryFormatError
	}
	if containsAny(why, "knowledge", "recall", "召回", "grounding", "unsupported", "hallucinat") {
		return CategoryKnowledgeRecall
	}
	if strings.Contains(name, "final_response") || strings.Contains(name, "rouge") ||
		strings.Contains(name, "response") {
		return CategoryResponseMismatch
	}
	return CategoryOther
}

// severityCounts aggregates terminal-loss severities across all rounds. Severity
// is only available on training-set terminal losses, not on validation metric
// results, so this reflects the optimization signal, not the validation failures.
func severityCounts(rounds []engine.RoundResult) map[string]int {
	counts := map[string]int{}
	for _, round := range rounds {
		for _, caseLoss := range round.Losses {
			for _, terminal := range caseLoss.TerminalLosses {
				severity := string(terminal.Severity)
				if severity == "" {
					severity = "unknown"
				}
				counts[severity]++
			}
		}
	}
	return counts
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
