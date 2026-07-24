//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestAttributeFailures(t *testing.T) {
	tests := []struct {
		name     string
		input    attributionInput
		expected failureCategory
	}{
		{
			name: "structured output",
			input: attributionInput{
				metrics:          []metricEvaluation{{Name: metricFinalResponse, Passed: false, Reason: "final response mismatch"}},
				actualResponse:   "value=42",
				expectedResponse: `{"value":42}`,
			},
			expected: failureFormat,
		},
		{
			name: "wrong tool",
			input: attributionInput{
				metrics: []metricEvaluation{{Name: metricToolTrajectory, Passed: false, Reason: "tool mismatch"}},
				actualTools: []toolAudit{
					{Name: "search_web", Arguments: map[string]any{"q": "weather"}},
				},
				expectedTools: []toolAudit{
					{Name: "lookup_record", Arguments: map[string]any{"q": "weather"}},
				},
			},
			expected: failureToolCall,
		},
		{
			name: "wrong arguments",
			input: attributionInput{
				metrics: []metricEvaluation{{Name: metricToolTrajectory, Passed: false, Reason: "arguments mismatch"}},
				actualTools: []toolAudit{
					{Name: "lookup_record", Arguments: map[string]any{"q": "shanghai"}},
				},
				expectedTools: []toolAudit{
					{Name: "lookup_record", Arguments: map[string]any{"q": "shenzhen"}},
				},
			},
			expected: failureToolArgument,
		},
		{
			name: "knowledge recall",
			input: attributionInput{
				metrics: []metricEvaluation{
					{Name: metricFinalResponse, Passed: false, Reason: "missing grounded answer"},
					{Name: metricToolTrajectory, Passed: false, Reason: "tool count mismatch"},
				},
				actualResponse:   "I do not have enough context.",
				expectedResponse: "Retention is 30 days.",
				expectedTools: []toolAudit{
					{Name: "knowledge_search", Arguments: map[string]any{"query": "retention"}},
				},
			},
			expected: failureKnowledgeRecall,
		},
		{
			name: "route trace",
			input: attributionInput{
				metrics: []metricEvaluation{{Name: metricFinalResponse, Passed: false, Reason: "wrong answer"}},
				trace: traceAudit{
					Steps: []traceStepAudit{{NodeID: "router", Error: "route selected billing instead of support"}},
				},
			},
			expected: failureRoute,
		},
		{
			name: "generic final response",
			input: attributionInput{
				metrics:          []metricEvaluation{{Name: metricFinalResponse, Passed: false, Reason: "text mismatch"}},
				actualResponse:   "blue",
				expectedResponse: "green",
			},
			expected: failureFinalResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attributions := attributeFailures(test.input)
			if len(attributions) == 0 {
				t.Fatal("attributeFailures returned no attribution")
			}
			if attributions[0].Category != test.expected {
				t.Fatalf("primary category = %q, want %q", attributions[0].Category, test.expected)
			}
			if attributions[0].Evidence == "" {
				t.Fatal("primary attribution evidence is empty")
			}
		})
	}
}

func TestAttributeFailuresAlwaysExplainsFailedCase(t *testing.T) {
	attributions := attributeFailures(attributionInput{
		metrics: []metricEvaluation{{Name: "custom_rubric", Passed: false}},
	})
	if len(attributions) != 1 {
		t.Fatalf("attribution count = %d, want 1", len(attributions))
	}
	if attributions[0].Category != failureUnknown {
		t.Fatalf("category = %q, want %q", attributions[0].Category, failureUnknown)
	}
}
