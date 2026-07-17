//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main implements the PromptIter regression loop example.
package main

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const (
	// FailureFinalResponseMismatch indicates the final answer does not match the expected response.
	FailureFinalResponseMismatch = "final_response_mismatch"
	// FailureToolCallError indicates the model called an unexpected tool or missed a required tool.
	FailureToolCallError = "tool_call_error"
	// FailureToolArgumentError indicates a tool call used incorrect arguments.
	FailureToolArgumentError = "tool_argument_error"
	// FailureRouteError indicates the response used the wrong route or handling path.
	FailureRouteError = "route_error"
	// FailureFormatError indicates the response violated the requested output format.
	FailureFormatError = "format_error"
	// FailureKnowledgeRecallGap indicates a factual or recall gap in the response.
	FailureKnowledgeRecallGap = "knowledge_recall_gap"
	// FailureUnknown is used when no more specific failure category matches.
	FailureUnknown = "unknown"
)

// AttributeFailures maps metric failures and trace signals to explainable categories.
func AttributeFailures(metrics []MetricResult, actual, expected Invocation) []FailureAttribution {
	var failures []FailureAttribution
	for _, metric := range metrics {
		if metric.Status == status.EvalStatusPassed {
			continue
		}
		category := classifyFailure(metric)
		evidence := strings.TrimSpace(metric.Reason)
		if evidence == "" {
			evidence = "metric failed without a detailed reason"
		}
		failures = append(failures, FailureAttribution{
			Category:   category,
			MetricName: metric.MetricName,
			Evidence:   evidence,
		})
	}
	if len(failures) == 0 {
		return nil
	}
	return dedupeFailures(failures)
}

func classifyFailure(metric MetricResult) string {
	reason := strings.ToLower(metric.Reason)
	switch {
	case strings.Contains(reason, "knowledge recall"):
		return FailureKnowledgeRecallGap
	case strings.Contains(reason, "format error"):
		return FailureFormatError
	case strings.Contains(reason, "route error"):
		return FailureRouteError
	case strings.Contains(reason, "tool argument"):
		return FailureToolArgumentError
	case strings.Contains(reason, "tool call") || strings.Contains(metric.MetricName, "tool"):
		return FailureToolCallError
	case strings.Contains(metric.MetricName, "final_response"):
		return FailureFinalResponseMismatch
	default:
		return FailureUnknown
	}
}

func dedupeFailures(failures []FailureAttribution) []FailureAttribution {
	seen := make(map[string]struct{}, len(failures))
	deduped := make([]FailureAttribution, 0, len(failures))
	for _, failure := range failures {
		key := failure.Category + "\x00" + failure.MetricName + "\x00" + failure.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, failure)
	}
	return deduped
}

func summarizeFailures(train, validation EvaluationRun) FailureSummary {
	return FailureSummary{
		Train:      countFailures(train),
		Validation: countFailures(validation),
	}
}

func countFailures(run EvaluationRun) map[string]int {
	counts := make(map[string]int)
	for _, evalCase := range run.Cases {
		for _, failure := range evalCase.FailureReasons {
			counts[failure.Category]++
		}
	}
	return counts
}
