// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAttributeFailuresClassifiesDeterministicSignals(t *testing.T) {
	tests := []struct {
		name       string
		metricName string
		reason     string
		kind       MetricKind
		trace      Trace
		message    string
		want       FailureCategory
	}{
		{name: "final response", metricName: "answer_quality", kind: MetricFinalResponse, want: FailureFinalResponse},
		{name: "tool call", metricName: "tool_trajectory", reason: "wrong tool selected", kind: MetricToolTrajectory, want: FailureToolCall},
		{name: "tool argument", metricName: "tool_trajectory", reason: "argument schema mismatch", kind: MetricToolTrajectory, want: FailureToolArgument},
		{name: "route", metricName: "router", reason: "wrong sub-agent handoff", kind: MetricRoute, want: FailureRoute},
		{name: "format", metricName: "json_format", reason: "invalid json format", kind: MetricFormat, want: FailureFormat},
		{name: "knowledge", metricName: "retrieval", reason: "knowledge recall was insufficient", kind: MetricKnowledge, want: FailureKnowledge},
		{name: "rubric response", metricName: "rubric_quality", reason: "rubric says the final response is unsupported", want: FailureFinalResponse},
		{name: "structured output", metricName: "structured_output_schema", reason: "structured output schema mismatch", want: FailureFormat},
		{name: "tool rubric argument", metricName: "tool_trajectory", reason: "rubric found an invalid argument", kind: MetricToolTrajectory, want: FailureToolArgument},
		{name: "execution", metricName: "answer_quality", trace: Trace{Status: "failed"}, message: "runner failed", want: FailureExecution},
		{name: "fallback", metricName: "business_metric", want: FailureMetric},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.trace.Status == "" {
				test.trace.Status = "completed"
			}
			result := &EvaluationResult{Cases: []CaseResult{{
				EvalSetID:    "validation",
				CaseID:       "case-1",
				ErrorMessage: test.message,
				Trace:        test.trace,
				Metrics: []MetricResult{{
					Name: test.metricName, Status: status.EvalStatusFailed, Reason: test.reason,
				}},
			}}}
			catalog := AttributionCatalog{MetricKinds: map[string]MetricKind{test.metricName: test.kind}}
			attribution := AttributeFailures(result, catalog)
			if len(attribution.Items) != 1 {
				t.Fatalf("got %d attribution items, want 1", len(attribution.Items))
			}
			if got := attribution.Items[0].Category; got != test.want {
				t.Fatalf("category = %q, want %q", got, test.want)
			}
			if attribution.Items[0].Reason == "" || len(attribution.Items[0].Evidence) == 0 {
				t.Fatal("failure attribution is not explainable")
			}
		})
	}
}

func TestAttributeFailuresDoesNotClassifyFromFinalResponseText(t *testing.T) {
	result := &EvaluationResult{Cases: []CaseResult{{
		EvalSetID: "validation",
		CaseID:    "case-1",
		Trace: Trace{
			Status: "completed",
			Output: "The answer mentions a wrong tool, route, and invalid JSON only as user-facing text.",
		},
		Metrics: []MetricResult{{
			Name: "final_response_avg_score", Status: status.EvalStatusFailed,
		}},
	}}}
	catalog := AttributionCatalog{MetricKinds: map[string]MetricKind{
		"final_response_avg_score": MetricFinalResponse,
	}}
	got := AttributeFailures(result, catalog)
	if len(got.Items) != 1 || got.Items[0].Category != FailureFinalResponse {
		t.Fatalf("attribution = %+v, want final response mismatch", got.Items)
	}
}
