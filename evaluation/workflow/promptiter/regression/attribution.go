//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// FailureCategory is the primary, deterministic cause assigned to a failed case.
type FailureCategory string

const (
	FailureExecutionError       FailureCategory = "execution_error"
	FailureRouteError           FailureCategory = "route_error"
	FailureToolCallError        FailureCategory = "tool_call_error"
	FailureToolArgumentError    FailureCategory = "tool_argument_error"
	FailureFormatError          FailureCategory = "format_error"
	FailureKnowledgeRecall      FailureCategory = "knowledge_recall_insufficient"
	FailureFinalResponse        FailureCategory = "final_response_mismatch"
	FailureOtherEvaluationError FailureCategory = "other_evaluation_failure"
)

// Failure explains why one case failed.
type Failure struct {
	CaseID      string          `json:"case_id"`
	Category    FailureCategory `json:"category"`
	MetricNames []string        `json:"metric_names"`
	Reason      string          `json:"reason"`
}

// FailureHint is the bounded feedback supplied to PromptIter.
type FailureHint struct {
	CaseID   string          `json:"case_id"`
	Category FailureCategory `json:"category"`
	Reason   string          `json:"reason"`
}

// Attribution contains failures and stable category totals.
type Attribution struct {
	Failures []Failure               `json:"failures"`
	Counts   map[FailureCategory]int `json:"counts"`
}

// Attribute assigns one primary cause to every failed case.
func Attribute(summary *EvalSummary) (*Attribution, error) {
	if summary == nil {
		return nil, errors.New("evaluation summary is nil")
	}
	result := &Attribution{Counts: make(map[FailureCategory]int)}
	for _, evalCase := range summary.Cases {
		if evalCase.Passed {
			continue
		}
		failure := attributeCase(evalCase)
		result.Failures = append(result.Failures, failure)
		result.Counts[failure.Category]++
	}
	sort.Slice(result.Failures, func(i, j int) bool { return result.Failures[i].CaseID < result.Failures[j].CaseID })
	return result, nil
}

// Hints converts attribution into concise PromptIter feedback.
func Hints(attribution *Attribution) ([]FailureHint, error) {
	if attribution == nil {
		return nil, errors.New("attribution is nil")
	}
	hints := make([]FailureHint, 0, len(attribution.Failures))
	for _, failure := range attribution.Failures {
		hints = append(hints, FailureHint{
			CaseID: failure.CaseID, Category: failure.Category,
			Reason: truncate(failure.Reason, 512),
		})
	}
	return hints, nil
}

func attributeCase(evalCase CaseSummary) Failure {
	failedMetrics := make([]MetricSummary, 0, len(evalCase.Metrics))
	metricNames := make([]string, 0, len(evalCase.Metrics))
	for _, metric := range evalCase.Metrics {
		if metric.Evaluated && !metric.Passed {
			failedMetrics = append(failedMetrics, metric)
			metricNames = append(metricNames, metric.Name)
		}
	}
	sort.Strings(metricNames)
	failure := Failure{CaseID: evalCase.ID, MetricNames: metricNames}
	switch {
	case evalCase.Error != "" || routeHasError(evalCase.ActualInvocations):
		failure.Category = FailureExecutionError
		failure.Reason = firstNonEmpty(evalCase.Error, firstRouteError(evalCase.ActualInvocations), "execution failed")
	case routesDiffer(evalCase.ActualInvocations, evalCase.ExpectedInvocations):
		failure.Category = FailureRouteError
		failure.Reason = "actual route does not match expected route"
	case toolCallsDiffer(evalCase.ActualInvocations, evalCase.ExpectedInvocations, toolOrderSensitive(failedMetrics)):
		failure.Category = FailureToolCallError
		failure.Reason = "actual tool names or order do not match expected trajectory"
	case toolArgumentsDiffer(evalCase.ActualInvocations, evalCase.ExpectedInvocations, toolOrderSensitive(failedMetrics)):
		failure.Category = FailureToolArgumentError
		failure.Reason = "tool arguments do not match expected arguments"
	case toolResultsDiffer(evalCase.ActualInvocations, evalCase.ExpectedInvocations, toolOrderSensitive(failedMetrics)):
		failure.Category = FailureToolCallError
		failure.Reason = "tool results do not match expected results"
	case formatFailure(failedMetrics):
		failure.Category = FailureFormatError
		failure.Reason = metricReasonText(failedMetrics, "response format did not satisfy the evaluator")
	case knowledgeFailure(failedMetrics):
		failure.Category = FailureKnowledgeRecall
		failure.Reason = metricReasonText(failedMetrics, "retrieved knowledge was insufficient")
	case hasCriterion(failedMetrics, "final_response"):
		failure.Category = FailureFinalResponse
		failure.Reason = metricReasonText(failedMetrics, "final response did not match the expected result")
	default:
		failure.Category = FailureOtherEvaluationError
		failure.Reason = metricReasonText(failedMetrics, "evaluation failed without a more specific cause")
	}
	failure.Reason = truncate(failure.Reason, 1024)
	return failure
}

func routeHasError(invocations []InvocationSummary) bool {
	return firstRouteError(invocations) != ""
}

func firstRouteError(invocations []InvocationSummary) string {
	for _, invocation := range invocations {
		for _, step := range invocation.Route {
			if strings.TrimSpace(step.Error) != "" {
				return step.Error
			}
		}
	}
	return ""
}

func routesDiffer(actual, expected []InvocationSummary) bool {
	if len(expected) == 0 {
		return false
	}
	hasExpectedRoute := false
	for _, invocation := range expected {
		hasExpectedRoute = hasExpectedRoute || len(invocation.Route) > 0
	}
	if !hasExpectedRoute {
		return false
	}
	if len(actual) != len(expected) {
		return true
	}
	for i := range actual {
		if len(actual[i].Route) != len(expected[i].Route) {
			return true
		}
		for j := range actual[i].Route {
			left, right := actual[i].Route[j], expected[i].Route[j]
			if left.Agent != right.Agent || left.Branch != right.Branch || left.NodeID != right.NodeID {
				return true
			}
		}
	}
	return false
}

func toolCallsDiffer(actual, expected []InvocationSummary, orderSensitive bool) bool {
	if len(expected) == 0 {
		return false
	}
	if len(actual) != len(expected) {
		return true
	}
	for i := range actual {
		if len(actual[i].Tools) != len(expected[i].Tools) {
			return true
		}
		actualNames, expectedNames := toolNames(actual[i].Tools), toolNames(expected[i].Tools)
		if !orderSensitive {
			sort.Strings(actualNames)
			sort.Strings(expectedNames)
		}
		if strings.Join(actualNames, "\x00") != strings.Join(expectedNames, "\x00") {
			return true
		}
	}
	return false
}

func toolArgumentsDiffer(actual, expected []InvocationSummary, orderSensitive bool) bool {
	if len(expected) == 0 || len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if len(actual[i].Tools) != len(expected[i].Tools) {
			return false
		}
		left, right := toolSignatures(actual[i].Tools), toolSignatures(expected[i].Tools)
		if !orderSensitive {
			sort.Strings(left)
			sort.Strings(right)
		}
		if strings.Join(left, "\x00") != strings.Join(right, "\x00") {
			return true
		}
	}
	return false
}

func toolResultsDiffer(actual, expected []InvocationSummary, orderSensitive bool) bool {
	if len(expected) == 0 || len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if len(actual[i].Tools) != len(expected[i].Tools) {
			return false
		}
		left, right := toolResultSignatures(actual[i].Tools), toolResultSignatures(expected[i].Tools)
		if !orderSensitive {
			sort.Strings(left)
			sort.Strings(right)
		}
		if strings.Join(left, "\x00") != strings.Join(right, "\x00") {
			return true
		}
	}
	return false
}

func toolOrderSensitive(metrics []MetricSummary) bool {
	for _, metric := range metrics {
		if metric.Criterion == "tool_trajectory" && metric.ToolOrderSensitive {
			return true
		}
	}
	return false
}

func toolNames(tools []ToolSummary) []string {
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		result = append(result, tool.Name)
	}
	return result
}

func toolSignatures(tools []ToolSummary) []string {
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		var value any
		if json.Unmarshal(tool.Arguments, &value) == nil {
			canonical, _ := json.Marshal(value)
			result = append(result, tool.Name+"\x00"+string(canonical))
		} else {
			result = append(result, tool.Name+"\x00"+string(tool.Arguments))
		}
	}
	return result
}

func toolResultSignatures(tools []ToolSummary) []string {
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		result = append(result, tool.Name+"\x00"+canonicalJSON(tool.Arguments)+"\x00"+canonicalJSON(tool.Result))
	}
	return result
}

func canonicalJSON(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	canonical, _ := json.Marshal(value)
	return string(canonical)
}

func formatFailure(metrics []MetricSummary) bool {
	for _, metric := range metrics {
		if structuredFieldMatches(metric, []string{"format", "json", "xml", "schema", "structured"}) {
			return true
		}
		reason := strings.ToLower(metric.Reason)
		for _, phrase := range []string{"invalid json", "json schema", "invalid xml", "output format", "structured output", "format mismatch"} {
			if strings.Contains(reason, phrase) {
				return true
			}
		}
	}
	return false
}

func knowledgeFailure(metrics []MetricSummary) bool {
	for _, metric := range metrics {
		if structuredFieldMatches(metric, []string{"knowledge", "recall", "grounding", "context", "citation"}) {
			return true
		}
		reason := strings.ToLower(metric.Reason)
		for _, phrase := range []string{"retrieved context", "missing citation", "insufficient context", "knowledge recall", "not grounded"} {
			if strings.Contains(reason, phrase) {
				return true
			}
		}
	}
	return false
}

func structuredFieldMatches(metric MetricSummary, wanted []string) bool {
	fields := append([]string{metric.Name, metric.Criterion}, metric.RubricTypes...)
	for _, field := range fields {
		tokens := strings.FieldsFunc(strings.ToLower(field), func(r rune) bool {
			return r == '_' || r == '-' || r == ' ' || r == '/' || r == '.'
		})
		for _, token := range tokens {
			for _, word := range wanted {
				if token == word {
					return true
				}
			}
		}
	}
	return false
}

func hasCriterion(metrics []MetricSummary, criterion string) bool {
	for _, metric := range metrics {
		if metric.Criterion == criterion {
			return true
		}
	}
	return false
}

func metricReasonText(metrics []MetricSummary, fallback string) string {
	for _, metric := range metrics {
		if metric.Reason != "" {
			return fmt.Sprintf("%s: %s", metric.Name, metric.Reason)
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
