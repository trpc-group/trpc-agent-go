//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pipeline contains the regression-loop logic that sits on top of the PromptIter engine:
// failure attribution, per-case delta classification, the multi-criterion acceptance gate, and
// audit-report generation. Everything here is pure logic over *evaluation.EvaluationResult so it
// can be unit-tested with synthetic results and reused independently of model/agent wiring.
package pipeline

import (
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// FailureCategory is the root-cause bucket a failing eval case is attributed to. The six real
// categories map to the observable signals the evaluation service exposes (criterion type plus the
// free-text reason); CategoryNone marks a passing case and CategoryUnknown an unclassifiable one.
type FailureCategory string

const (
	// CategoryNone is used for a case that passed; it has no failure to attribute.
	CategoryNone FailureCategory = ""
	// CategoryResponseMismatch means the final response content did not match the expectation
	// (text/rouge/structurally-valid-but-wrong JSON). This is a "said the wrong thing" defect.
	CategoryResponseMismatch FailureCategory = "response_mismatch"
	// CategoryToolCallError means the tool trajectory was wrong in shape: a missing, extra, or
	// out-of-order tool call. The agent called the wrong set/sequence of tools.
	CategoryToolCallError FailureCategory = "tool_call_error"
	// CategoryToolArgError means the right tool was called but with wrong arguments.
	CategoryToolArgError FailureCategory = "tool_arg_error"
	// CategoryRouteError means the agent routed to the wrong tool (wrong tool name).
	CategoryRouteError FailureCategory = "route_error"
	// CategoryFormatError means the output was structurally invalid: unparsable JSON/XML, or a
	// judge response the evaluator could not parse. A "shape is broken" defect, not a content one.
	CategoryFormatError FailureCategory = "format_error"
	// CategoryKnowledgeRecall means an LLM judge / rubric scored the answer as missing or
	// incorrect knowledge (the answer was well-formed but factually inadequate).
	CategoryKnowledgeRecall FailureCategory = "knowledge_recall"
	// CategoryUnknown means the case failed but no signal was strong enough to classify it.
	CategoryUnknown FailureCategory = "unknown"
)

// CaseAttribution is the attributed diagnosis for a single eval case.
type CaseAttribution struct {
	// EvalCaseID identifies the eval case.
	EvalCaseID string
	// Passed reports whether the case's overall status was passed.
	Passed bool
	// Score is the aggregated case score (mean of metric scores), in [0,1].
	Score float64
	// Category is the attributed failure category; CategoryNone when Passed is true.
	Category FailureCategory
	// MetricName is the metric that drove the classification (the first failing metric).
	MetricName string
	// Reason is the evaluator's free-text reason for the driving metric, kept for the report.
	Reason string
}

// AttributeResult classifies every case in an evaluation result. Cases appear in the same order as
// result.EvalCases. A nil result yields a nil slice.
func AttributeResult(result *evaluation.EvaluationResult) []CaseAttribution {
	if result == nil {
		return nil
	}
	attributions := make([]CaseAttribution, 0, len(result.EvalCases))
	for _, evalCase := range result.EvalCases {
		attributions = append(attributions, attributeCase(evalCase))
	}
	return attributions
}

// CategoryCount is the number of failing cases attributed to one failure category.
type CategoryCount struct {
	Category FailureCategory `json:"category"`
	Count    int             `json:"count"`
}

// AttributionStats aggregates the failure attributions of a result into per-category counts. Only
// failing categories are counted (passing cases are excluded). The result is sorted by category
// name for deterministic output. A result with no failures yields an empty (non-nil) slice.
func AttributionStats(result *evaluation.EvaluationResult) []CategoryCount {
	counts := map[FailureCategory]int{}
	for _, a := range AttributeResult(result) {
		if a.Passed || a.Category == CategoryNone {
			continue
		}
		counts[a.Category]++
	}
	stats := make([]CategoryCount, 0, len(counts))
	for category, count := range counts {
		stats = append(stats, CategoryCount{Category: category, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Category < stats[j].Category })
	return stats
}

// attributeCase diagnoses a single case. A passing case gets CategoryNone; a failing case is
// classified from its first failing metric (the metrics are checked in declared order).
func attributeCase(c *evaluation.EvaluationCaseResult) CaseAttribution {
	if c == nil {
		return CaseAttribution{Category: CategoryUnknown}
	}
	attribution := CaseAttribution{
		EvalCaseID: c.EvalCaseID,
		Passed:     c.OverallStatus == status.EvalStatusPassed,
		Score:      caseScore(c),
	}
	if attribution.Passed {
		attribution.Category = CategoryNone
		return attribution
	}
	driver := firstFailingMetric(c.MetricResults)
	if driver == nil {
		attribution.Category = CategoryUnknown
		return attribution
	}
	attribution.MetricName = driver.MetricName
	if driver.Details != nil {
		attribution.Reason = driver.Details.Reason
	}
	attribution.Category = classifyMetric(driver)
	return attribution
}

// caseScore returns the mean of the case's aggregated metric scores. With the single-metric sample
// data this is just that metric's score; averaging keeps it sensible for multi-metric sets.
func caseScore(c *evaluation.EvaluationCaseResult) float64 {
	if len(c.MetricResults) == 0 {
		return 0
	}
	var sum float64
	for _, m := range c.MetricResults {
		sum += m.Score
	}
	return sum / float64(len(c.MetricResults))
}

// firstFailingMetric returns the first metric whose status is failed, or nil when none failed.
func firstFailingMetric(metrics []*evalresult.EvalMetricResult) *evalresult.EvalMetricResult {
	for _, m := range metrics {
		if m != nil && m.EvalStatus == status.EvalStatusFailed {
			return m
		}
	}
	return nil
}

// classifyMetric attributes a single failing metric to a category. The primary signal is the
// criterion type; the free-text reason disambiguates sub-categories that share a criterion type
// (tool call vs arg vs route) or that a structural criterion can produce (content vs format).
func classifyMetric(m *evalresult.EvalMetricResult) FailureCategory {
	if m == nil {
		return CategoryUnknown
	}
	reason := ""
	if m.Details != nil {
		reason = strings.ToLower(m.Details.Reason)
	}
	c := m.Criterion
	switch {
	case c != nil && c.ToolTrajectory != nil:
		return classifyToolTrajectory(reason)
	case c != nil && c.FinalResponse != nil:
		return classifyFinalResponse(c.FinalResponse.JSON != nil || c.FinalResponse.XML != nil, reason)
	case c != nil && c.LLMJudge != nil:
		if mentionsFormatDefect(reason) {
			return CategoryFormatError
		}
		return CategoryKnowledgeRecall
	default:
		return classifyByReason(reason)
	}
}

// classifyFinalResponse distinguishes a broken-shape failure (invalid JSON/XML) from a
// wrong-content failure. structural reports whether the criterion validates JSON or XML.
func classifyFinalResponse(structural bool, reason string) FailureCategory {
	if structural && mentionsFormatDefect(reason) {
		return CategoryFormatError
	}
	return CategoryResponseMismatch
}

// classifyToolTrajectory splits a tool-trajectory failure into arg / route / call-shape errors.
// Argument and route defects are checked first because a plain trajectory mismatch (missing, extra,
// or reordered call) is the residual default.
func classifyToolTrajectory(reason string) FailureCategory {
	switch {
	case mentionsArgDefect(reason):
		return CategoryToolArgError
	case mentionsRouteDefect(reason):
		return CategoryRouteError
	default:
		return CategoryToolCallError
	}
}

// classifyByReason is the fallback when the criterion type is absent: it scans the reason text for
// the strongest category signal, checking the more specific buckets before the general ones.
func classifyByReason(reason string) FailureCategory {
	switch {
	case mentionsFormatDefect(reason):
		return CategoryFormatError
	case mentionsArgDefect(reason):
		return CategoryToolArgError
	case mentionsRouteDefect(reason):
		return CategoryRouteError
	case mentionsToolCallDefect(reason):
		return CategoryToolCallError
	case mentionsKnowledgeDefect(reason):
		return CategoryKnowledgeRecall
	case mentionsResponseDefect(reason):
		return CategoryResponseMismatch
	default:
		return CategoryUnknown
	}
}

func containsAny(text string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

func mentionsFormatDefect(reason string) bool {
	return containsAny(reason, "invalid json", "invalid xml", "not valid", "malformed",
		"unparsable", "unmarshal", "parse error", "failed to parse", "schema")
}

func mentionsArgDefect(reason string) bool {
	return containsAny(reason, "argument", "arguments", "parameter", "param mismatch", "arg mismatch")
}

func mentionsRouteDefect(reason string) bool {
	return containsAny(reason, "tool name", "wrong tool", "unexpected tool", "route", "wrong route")
}

func mentionsToolCallDefect(reason string) bool {
	return containsAny(reason, "tool call", "tool trajectory", "sequence", "order", "missing call",
		"extra call", "expected tool")
}

func mentionsKnowledgeDefect(reason string) bool {
	return containsAny(reason, "rubric", "knowledge", "incorrect fact", "missing information", "hallucinat")
}

func mentionsResponseDefect(reason string) bool {
	return containsAny(reason, "final response", "response mismatch", "did not match", "mismatch", "expected")
}
