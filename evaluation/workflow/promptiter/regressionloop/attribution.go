//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"fmt"
	"strconv"
	"strings"
)

// AttributeCase returns deterministic failure attributions for a case.
func AttributeCase(c CaseResult) []Attribution {
	if c.Passed && !c.HardFail {
		if c.Attributions == nil {
			return []Attribution{}
		}
		return c.Attributions
	}
	if len(c.Attributions) > 0 {
		return c.Attributions
	}
	for _, rule := range attributionRules() {
		text := strings.ToLower(sourceTextForRule(c, rule.source))
		if containsAny(text, rule.needles...) {
			return []Attribution{{
				Category:   rule.category,
				Confidence: rule.confidence,
				Evidence:   bestEvidence(c, rule.source),
				MetricName: firstMetricName(c),
				Source:     rule.source,
			}}
		}
	}
	if len(c.MetricResults) > 0 {
		return []Attribution{{
			Category:   AttributionMetricThresholdMiss,
			Confidence: 0.6,
			Evidence:   bestEvidence(c, "metric"),
			MetricName: firstMetricName(c),
			Source:     "metric",
		}}
	}
	return []Attribution{{
		Category:   AttributionUnknown,
		Confidence: 0.2,
		Evidence:   bestEvidence(c, "unknown"),
		Source:     "unknown",
	}}
}

// AttributeEvaluation attaches attributions to every failed case.
func AttributeEvaluation(summary EvaluationSummary) EvaluationSummary {
	for i := range summary.Cases {
		summary.Cases[i].Attributions = AttributeCase(summary.Cases[i])
	}
	return summary
}

type attributionRule struct {
	category   AttributionCategory
	source     string
	confidence float64
	needles    []string
}

func attributionRules() []attributionRule {
	return []attributionRule{
		{AttributionToolArgumentError, "tool_trajectory", 0.9, []string{"tool argument", "argument", "parameter", "param", "wrong query", "arguments mismatch"}},
		{AttributionToolSelectionError, "tool_trajectory", 0.9, []string{"wrong tool", "missing tool", "unexpected tool", "tool selection", "tool trajectory"}},
		{AttributionRoutingError, "trace", 0.85, []string{"route", "routing", "wrong agent", "wrong skill", "wrong node"}},
		{AttributionFormatError, "structured_output", 0.85, []string{"format", "json", "xml", "schema", "structured output", "markdown"}},
		{AttributionKnowledgeRecallInsufficient, "rubric", 0.8, []string{"knowledge", "recall", "missing context", "stale", "unsupported", "retrieval"}},
		{AttributionFinalResponseMismatch, "final_response", 0.8, []string{"final response", "mismatch", "incorrect answer", "wrong answer", "reference", "expected response"}},
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func evidenceParts(c CaseResult) []string {
	parts := append([]string{}, c.FailureReasons...)
	for _, metric := range c.MetricResults {
		parts = append(parts, metric.Name, metric.Reason)
	}
	return parts
}

func sourceTextForRule(c CaseResult, source string) string {
	parts := evidenceParts(c)
	switch source {
	case "tool_trajectory":
		parts = append(parts, toolCallsText(c.ToolTrajectory), toolCallsText(c.ExpectedToolTrajectory))
	case "trace":
		parts = append(parts, c.TraceSummary)
	case "structured_output":
		parts = append(parts, c.StructuredOutputStatus)
	case "rubric":
		parts = append(parts, c.RubricReason)
	case "final_response":
		parts = append(parts, c.FinalResponse, c.ExpectedResponse)
	}
	return strings.Join(parts, " ")
}

func toolCallsText(calls []ToolCall) string {
	parts := make([]string, 0, len(calls)*3)
	for _, call := range calls {
		parts = append(parts, call.Name, fmt.Sprint(call.Arguments), fmt.Sprint(call.Result))
	}
	return strings.Join(parts, " ")
}

func bestEvidence(c CaseResult, source string) string {
	switch source {
	case "final_response":
		if c.FinalResponse != "" || c.ExpectedResponse != "" {
			return "actual final response " + quote(c.FinalResponse) + " vs expected " + quote(c.ExpectedResponse)
		}
	case "tool_trajectory":
		if len(c.ToolTrajectory) > 0 || len(c.ExpectedToolTrajectory) > 0 {
			return "tool trajectory differs from expected trajectory"
		}
	case "trace":
		if c.TraceSummary != "" {
			return c.TraceSummary
		}
	case "structured_output":
		if c.StructuredOutputStatus != "" {
			return c.StructuredOutputStatus
		}
	case "rubric":
		if c.RubricReason != "" {
			return c.RubricReason
		}
	}
	for _, reason := range c.FailureReasons {
		if strings.TrimSpace(reason) != "" {
			return reason
		}
	}
	for _, metric := range c.MetricResults {
		if strings.TrimSpace(metric.Reason) != "" {
			return metric.Reason
		}
	}
	if c.FinalResponse != "" {
		return "actual final response " + quote(c.FinalResponse)
	}
	return "failure evidence unavailable; classified as unknown"
}

func firstMetricName(c CaseResult) string {
	for _, metric := range c.MetricResults {
		if metric.Name != "" {
			return metric.Name
		}
	}
	return ""
}

func quote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	return strconv.Quote(value)
}
