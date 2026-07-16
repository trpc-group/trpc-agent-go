//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"fmt"
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
					Reason:     explainReason(m.MetricName, m.Reason),
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

// explainReason returns the evaluator reason, or a stable fallback when it is
// empty, so every failure carries at least one explainable reason.
func explainReason(metricName, reason string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fmt.Sprintf("metric %s failed without evaluator reason", metricName)
}

// categorize maps one failed metric to a failure category. It checks reason
// signals (which distinguish argument/route/tool-call failures) before falling
// back to the metric name and family, so classification does not depend on a
// single exact keyword.
func categorize(metricName, reason string) FailureCategory {
	name := strings.ToLower(metricName)
	why := strings.ToLower(reason)
	toolMetric := containsAny(name, "tool_trajectory", "trajectory", "tool")

	// Tool called with wrong arguments (before the generic tool-call check).
	if containsAny(why, "argument", "参数", "arg ", "wrong parameter", "错误参数", "invalid arg") {
		return CategoryToolArgError
	}
	// Routing / wrong-agent / transfer failures.
	if containsAny(why, "route", "路由", "wrong agent", "错误的智能体", "错误智能体", "transfer", "转交", "misroute", "dispatch") {
		return CategoryRouteError
	}
	// Missing / wrong tool call (by reason or by tool-family metric).
	if toolMetric || containsAny(why, "tool call", "tool-call", "工具调用", "missing tool", "未调用工具", "wrong tool", "调错工具", "tool use") {
		return CategoryToolError
	}
	// Format / structure compliance.
	if containsAny(why, "format", "格式", "markdown", "schema", "xml", "json", "malformed", "结构", "不合法") {
		return CategoryFormatError
	}
	// Knowledge recall / grounding.
	if containsAny(why, "knowledge", "recall", "召回", "grounding", "unsupported", "hallucinat", "知识", "编造", "凭空", "证据不足", "无依据") {
		return CategoryKnowledgeRecall
	}
	// Final-response content mismatch.
	if containsAny(name, "final_response", "rouge", "response") ||
		containsAny(why, "mismatch", "不匹配", "答案错误", "回复错误", "内容错误") {
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
