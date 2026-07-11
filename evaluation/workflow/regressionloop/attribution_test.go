//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAttributeFailure_ToolTrajectory(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case1",
		MetricName: "tool_trajectory_avg_score",
		Score:      0.3,
		Reason:     "tool call sequence did not match expected trajectory",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureToolCallError {
		t.Errorf("expected %s, got %s", FailureToolCallError, attr.Category)
	}
	if attr.EvalSetID != "set1" || attr.EvalCaseID != "case1" {
		t.Errorf("eval IDs not preserved: got %s/%s", attr.EvalSetID, attr.EvalCaseID)
	}
	if attr.Score != 0.3 {
		t.Errorf("score not preserved: got %f", attr.Score)
	}
}

func TestAttributeFailure_FinalResponse(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case2",
		MetricName: "final_response_avg_score",
		Score:      0.2,
		Reason:     "response did not match expected output",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureFinalResponseMismatch {
		t.Errorf("expected %s, got %s", FailureFinalResponseMismatch, attr.Category)
	}
}

func TestAttributeFailure_Hallucination(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case3",
		MetricName: "hallucination_avg_score",
		Score:      0.1,
		Reason:     "response contains hallucinated content not grounded in context",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureHallucination {
		t.Errorf("expected %s, got %s", FailureHallucination, attr.Category)
	}
}

func TestAttributeFailure_KnowledgeRecall(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case4",
		MetricName: "rubric_knowledge_recall_avg_score",
		Score:      0.4,
		Reason:     "insufficient context to answer the question",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureKnowledgeRecall {
		t.Errorf("expected %s, got %s", FailureKnowledgeRecall, attr.Category)
	}
}

func TestAttributeFailure_ToolArgError_ByReason(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case5",
		MetricName: "some_custom_metric",
		Score:      0.0,
		Reason:     "agent called the right tool but with wrong argument values",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureToolArgumentError {
		t.Errorf("expected %s, got %s", FailureToolArgumentError, attr.Category)
	}
}

func TestAttributeFailure_RouteError_ByReason(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case6",
		MetricName: "custom_metric",
		Score:      0.0,
		Reason:     "agent was routed to wrong sub-agent for this task",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureRouteError {
		t.Errorf("expected %s, got %s", FailureRouteError, attr.Category)
	}
}

func TestAttributeFailure_FormatError_ByReason(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case7",
		MetricName: "format_checker",
		Score:      0.0,
		Reason:     "output has invalid JSON structure",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureFormatError {
		t.Errorf("expected %s, got %s", FailureFormatError, attr.Category)
	}
}

func TestAttributeFailure_Fallback_QualityBelowThreshold(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case8",
		MetricName: "custom_quality_metric",
		Score:      0.4,
		Reason:     "the output quality was below expectations",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureQualityBelowThreshold {
		t.Errorf("expected %s, got %s", FailureQualityBelowThreshold, attr.Category)
	}
}

func TestAttributeFailure_Fallback_Unknown(t *testing.T) {
	input := AttributionInput{
		EvalSetID:  "set1",
		EvalCaseID: "case9",
		MetricName: "custom_metric",
		Score:      0.0,
		Reason:     "something went wrong",
		Status:     string(status.EvalStatusFailed),
	}
	attr := AttributeFailure(input)
	if attr.Category != FailureUnknown {
		t.Errorf("expected %s, got %s", FailureUnknown, attr.Category)
	}
	if attr.Explanation != "no classification rule matched" {
		t.Errorf("unexpected explanation: %s", attr.Explanation)
	}
}

func TestAttributeFailures_MultipleMetrics(t *testing.T) {
	results := []CaseEvalResult{
		{
			EvalSetID:  "set1",
			EvalCaseID: "case1",
			Metrics: []MetricInfo{
				{MetricName: "tool_trajectory_avg_score", Score: 0.5, Status: string(status.EvalStatusFailed), Reason: "wrong tool called"},
				{MetricName: "final_response_avg_score", Score: 0.8, Status: string(status.EvalStatusPassed), Reason: "ok"},
			},
		},
		{
			EvalSetID:  "set1",
			EvalCaseID: "case2",
			Metrics: []MetricInfo{
				{MetricName: "hallucination_avg_score", Score: 0.1, Status: string(status.EvalStatusFailed), Reason: "hallucinated facts"},
			},
		},
	}
	attrs := AttributeFailures(results)
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attributions, got %d", len(attrs))
	}
	// First should be tool_call_error (from metric name match).
	if attrs[0].Category != FailureToolCallError {
		t.Errorf("expected tool_call_error for first attr, got %s", attrs[0].Category)
	}
	// Second should be hallucination (from metric name match).
	if attrs[1].Category != FailureHallucination {
		t.Errorf("expected hallucination for second attr, got %s", attrs[1].Category)
	}
}

func TestAttributeFailures_SkipsPassed(t *testing.T) {
	results := []CaseEvalResult{
		{
			EvalSetID:  "set1",
			EvalCaseID: "case1",
			Metrics: []MetricInfo{
				{MetricName: "m1", Score: 0.9, Status: string(status.EvalStatusPassed), Reason: ""},
				{MetricName: "m2", Score: 0.95, Status: string(status.EvalStatusPassed), Reason: ""},
			},
		},
	}
	attrs := AttributeFailures(results)
	if len(attrs) != 0 {
		t.Errorf("expected 0 attributions for all-passed case, got %d", len(attrs))
	}
}

func TestSummarizeAttributions(t *testing.T) {
	attrs := []FailureAttribution{
		{Category: FailureToolCallError, EvalSetID: "s1", EvalCaseID: "c1"},
		{Category: FailureToolCallError, EvalSetID: "s1", EvalCaseID: "c2"},
		{Category: FailureHallucination, EvalSetID: "s1", EvalCaseID: "c3"},
		{Category: FailureFinalResponseMismatch, EvalSetID: "s1", EvalCaseID: "c1"},
		// Duplicate case reference within same category should be deduplicated.
		{Category: FailureToolCallError, EvalSetID: "s1", EvalCaseID: "c1"},
	}
	summaries := SummarizeAttributions(attrs)
	if len(summaries) != 3 {
		t.Fatalf("expected 3 category summaries, got %d", len(summaries))
	}
	// Find tool_call_error summary.
	var toolSummary *AttributionSummary
	for i := range summaries {
		if summaries[i].Category == FailureToolCallError {
			toolSummary = &summaries[i]
			break
		}
	}
	if toolSummary == nil {
		t.Fatal("missing tool_call_error summary")
	}
	if toolSummary.Count != 3 {
		t.Errorf("expected 3 tool_call_error attributions, got %d", toolSummary.Count)
	}
	// Cases should be deduplicated: c1 and c2.
	if len(toolSummary.Cases) != 2 {
		t.Errorf("expected 2 unique cases for tool_call_error, got %d: %v", len(toolSummary.Cases), toolSummary.Cases)
	}
}

func TestAttributeFailure_KeywordDetection_ToolCall(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected FailureCategory
	}{
		{"wrong tool", "the agent called the wrong tool for this task", FailureToolCallError},
		{"did not call", "agent did not call the required function", FailureToolCallError},
		{"missing tool call", "missing tool call to get_weather", FailureToolCallError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := AttributionInput{
				MetricName: "unknown_metric",
				Reason:     tc.reason,
				Score:      0.0,
				Status:     string(status.EvalStatusFailed),
			}
			attr := AttributeFailure(input)
			if attr.Category != tc.expected {
				t.Errorf("reason=%q: expected %s, got %s", tc.reason, tc.expected, attr.Category)
			}
		})
	}
}

func TestAttributeFailure_KeywordDetection_FormatError(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{"format keyword", "output format does not match expected schema"},
		{"invalid json", "response is invalid json"},
		{"too long", "response is too long for the expected format"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := AttributionInput{
				MetricName: "unknown_metric",
				Reason:     tc.reason,
				Score:      0.0,
				Status:     string(status.EvalStatusFailed),
			}
			attr := AttributeFailure(input)
			if attr.Category != FailureFormatError {
				t.Errorf("reason=%q: expected %s, got %s", tc.reason, FailureFormatError, attr.Category)
			}
		})
	}
}

func TestAttributeFailure_KeywordDetection_Hallucination(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{"hallucination", "the response hallucinated information not in context"},
		{"fabricated", "agent fabricated data not present in source"},
		{"not grounded", "claims are not grounded in provided context"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := AttributionInput{
				MetricName: "some_metric",
				Reason:     tc.reason,
				Score:      0.0,
				Status:     string(status.EvalStatusFailed),
			}
			attr := AttributeFailure(input)
			if attr.Category != FailureHallucination {
				t.Errorf("reason=%q: expected %s, got %s", tc.reason, FailureHallucination, attr.Category)
			}
		})
	}
}
