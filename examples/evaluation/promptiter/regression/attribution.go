//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func attributeFailures(input attributionInput) []failureAttribution {
	attributions := make([]failureAttribution, 0, 4)
	seen := make(map[failureCategory]struct{})
	add := func(category failureCategory, evidence string) {
		if _, ok := seen[category]; ok {
			return
		}
		evidence = strings.TrimSpace(evidence)
		if evidence == "" {
			evidence = "failed evaluation signal matched this category"
		}
		seen[category] = struct{}{}
		attributions = append(attributions, failureAttribution{
			Category: category,
			Evidence: evidence,
		})
	}

	if knowledgeFailure(input) {
		add(failureKnowledgeRecall, "knowledge_search was missing or did not provide the expected grounded answer")
	}
	if expectsStructuredOutput(input.expectedResponse) && input.actualResponse != input.expectedResponse {
		if json.Valid([]byte(strings.TrimSpace(input.actualResponse))) {
			add(failureFormat, "structured response JSON does not match the expected output")
		} else {
			add(failureFormat, "expected valid JSON but the final response was not valid JSON")
		}
	}

	attributeToolFailures(input, add)
	if traceShowsRouteFailure(input.trace) || reasonsContain(input.metrics, "route", "router", "transfer", "handoff") {
		add(failureRoute, "execution trace or metric reason indicates an incorrect route")
	}
	if reasonsContain(input.metrics, "format", "json", "schema", "xml", "structured") {
		add(failureFormat, "metric reason indicates a structured output or format violation")
	}
	if finalResponseFailed(input.metrics) && input.actualResponse != input.expectedResponse {
		add(failureFinalResponse, "actual final response does not match the expected response")
	}

	if len(attributions) == 0 {
		add(failureUnknown, firstFailureReason(input.metrics))
	}
	return attributions
}

func knowledgeFailure(input attributionInput) bool {
	for _, tool := range input.expectedTools {
		if strings.Contains(strings.ToLower(tool.Name), "knowledge") {
			if len(input.actualTools) == 0 {
				return true
			}
			for _, actual := range input.actualTools {
				if actual.Name == tool.Name &&
					jsonValuesEqual(actual.Arguments, tool.Arguments) &&
					jsonValuesEqual(actual.Result, tool.Result) {
					return false
				}
			}
			return true
		}
	}
	return reasonsContain(input.metrics, "knowledge", "recall", "grounding", "grounded")
}

func attributeToolFailures(input attributionInput, add func(failureCategory, string)) {
	switch {
	case len(input.expectedTools) == 0 && len(input.actualTools) > 0:
		add(failureRoute, fmt.Sprintf("unexpected tool %q was called", input.actualTools[0].Name))
		return
	case len(input.expectedTools) > 0 && len(input.actualTools) == 0:
		add(failureRoute, fmt.Sprintf("expected tool %q was not called", input.expectedTools[0].Name))
		add(failureToolCall, "tool trajectory is missing an expected call")
		return
	case len(input.expectedTools) != len(input.actualTools):
		add(failureToolCall, fmt.Sprintf(
			"actual tool count %d does not match expected count %d",
			len(input.actualTools),
			len(input.expectedTools),
		))
	}

	for i := 0; i < len(input.actualTools) && i < len(input.expectedTools); i++ {
		actual := input.actualTools[i]
		expected := input.expectedTools[i]
		if actual.Name != expected.Name {
			add(failureToolCall, fmt.Sprintf(
				"actual tool %q does not match expected tool %q",
				actual.Name,
				expected.Name,
			))
			continue
		}
		if !jsonValuesEqual(actual.Arguments, expected.Arguments) {
			add(failureToolArgument, fmt.Sprintf("arguments for tool %q do not match", actual.Name))
		}
		if !jsonValuesEqual(actual.Result, expected.Result) {
			add(failureToolCall, fmt.Sprintf("result for tool %q does not match", actual.Name))
		}
	}
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return reflect.DeepEqual(left, right)
	}
	var leftValue any
	var rightValue any
	if json.Unmarshal(leftJSON, &leftValue) != nil || json.Unmarshal(rightJSON, &rightValue) != nil {
		return string(leftJSON) == string(rightJSON)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func expectsStructuredOutput(response string) bool {
	trimmed := strings.TrimSpace(response)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func traceShowsRouteFailure(trace traceAudit) bool {
	for _, step := range trace.Steps {
		text := strings.ToLower(strings.Join([]string{step.NodeID, step.NodeType, step.Error}, " "))
		if strings.Contains(text, "route") && step.Error != "" {
			return true
		}
	}
	return false
}

func reasonsContain(metrics []metricEvaluation, words ...string) bool {
	for _, metric := range metrics {
		if metric.Passed {
			continue
		}
		reason := strings.ToLower(metric.Reason)
		for _, word := range words {
			if strings.Contains(reason, strings.ToLower(word)) {
				return true
			}
		}
	}
	return false
}

func finalResponseFailed(metrics []metricEvaluation) bool {
	for _, metric := range metrics {
		if metric.Name == metricFinalResponse && !metric.Passed {
			return true
		}
	}
	return false
}

func firstFailureReason(metrics []metricEvaluation) string {
	for _, metric := range metrics {
		if metric.Passed {
			continue
		}
		if reason := strings.TrimSpace(metric.Reason); reason != "" {
			return reason
		}
		return fmt.Sprintf("metric %q failed without a detailed reason", metric.Name)
	}
	return "case failed without a recognized evaluation signal"
}
