//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"errors"
	"slices"
	"testing"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAttributeFailuresUsesStructuredEvidence(t *testing.T) {
	actual := &evalset.Invocation{
		FinalResponse: message(`not-json`),
		Tools:         []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{"id": "wrong"}, Result: map[string]any{"error": "timeout"}}},
	}
	expected := &evalset.Invocation{
		FinalResponse: message(`{"ok":true}`),
		Tools:         []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{"id": "right"}}},
	}
	result := &engine.EvaluationResult{EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{{
		EvalCaseID: "case", ActualInvocations: []*evalset.Invocation{actual}, ExpectedInvocations: []*evalset.Invocation{expected},
		Trace: &atrace.Trace{Steps: []atrace.Step{{AgentName: "wrong", Error: "trace failure"}}},
		Metrics: []engine.MetricResult{
			{MetricName: "final_response", Status: status.EvalStatusFailed, Reason: "mismatch"},
			{MetricName: "json_schema", Status: status.EvalStatusFailed, Reason: "invalid"},
			{MetricName: "tooltrajectory", Status: status.EvalStatusFailed, Reason: "arguments differ"},
		},
	}}}}}
	got := AttributeFailures(result, AttributionOptions{ExpectedAgentNames: map[string]string{"case": "expected"}})
	codes := make([]string, 0, len(got[0].Reasons))
	for _, reason := range got[0].Reasons {
		codes = append(codes, reason.Code)
	}
	for _, code := range []string{FailureFinalResponseMismatch, FailureFormatError, FailureToolArgumentError, FailureToolExecutionError, FailureRoutingError, FailureExecutionError} {
		if !slices.Contains(codes, code) {
			t.Errorf("missing %q in %#v", code, codes)
		}
	}
}

func TestAttributionHelperEdgeCases(t *testing.T) {
	if got := AttributeFailures(nil, AttributionOptions{}); len(got) != 0 {
		t.Fatalf("nil result attribution = %#v", got)
	}
	if !messagesEqual(nil, nil) || messagesEqual(nil, &evalset.Invocation{}) {
		t.Fatal("messagesEqual mishandled nil invocations")
	}
	if !invalidJSONResponse(nil) || !invalidJSONResponse(&evalset.Invocation{}) {
		t.Fatal("missing final response was considered valid JSON")
	}
	if reasons := compareTools(nil, &evalset.Invocation{}); len(reasons) != 1 || reasons[0].Code != FailureToolCallError {
		t.Fatalf("nil tool comparison = %#v", reasons)
	}
	if reasons := compareTools(&evalset.Invocation{}, &evalset.Invocation{Tools: []*evalset.Tool{{Name: "expected"}}}); len(reasons) != 1 || reasons[0].Code != FailureToolCallError {
		t.Fatalf("tool count comparison = %#v", reasons)
	}
	if reasons := compareTools(
		&evalset.Invocation{Tools: []*evalset.Tool{{Name: "actual"}}},
		&evalset.Invocation{Tools: []*evalset.Tool{{Name: "expected"}}},
	); len(reasons) != 1 || reasons[0].Code != FailureToolCallError {
		t.Fatalf("tool name comparison = %#v", reasons)
	}
	if got := toolExecutionError(errors.New("failed")); got != "failed" {
		t.Fatalf("error result = %q", got)
	}
	if got := toolExecutionError(map[string]any{"error_message": "failed"}); got != "failed" {
		t.Fatalf("structured error result = %q", got)
	}
	if got := toolExecutionError(map[string]any{"status": "ok"}); got != "" {
		t.Fatalf("successful result error = %q", got)
	}
}

func TestClassifyMetricCoversFailureCategories(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		code   string
	}{
		{name: "json_schema", code: FailureFormatError},
		{name: "tooltrajectory", reason: "argument mismatch", code: FailureToolArgumentError},
		{name: "tooltrajectory", reason: "execution error", code: FailureToolExecutionError},
		{name: "tooltrajectory", reason: "different call", code: FailureToolCallError},
		{name: "rouge", code: FailureFinalResponseMismatch},
		{name: "custom", reason: "wrong agent route", code: FailureRoutingError},
		{name: "custom", code: FailureEvaluationMismatch},
		{name: "custom", reason: `{"code":"tool_arg_error","message":"bad argument"}`, code: FailureToolArgumentError},
	}
	for _, test := range tests {
		code, message := classifyMetric(test.name, test.reason)
		if code != test.code || message == "" {
			t.Errorf("classifyMetric(%q, %q) = (%q, %q), want code %q", test.name, test.reason, code, message, test.code)
		}
	}
}

func TestAttributeFailuresAlwaysExplainsFailedCase(t *testing.T) {
	result := &engine.EvaluationResult{EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{{EvalCaseID: "case", Metrics: []engine.MetricResult{{MetricName: "custom", Status: status.EvalStatusFailed}}}}}}}
	got := AttributeFailures(result, AttributionOptions{})
	if len(got) != 1 || len(got[0].Reasons) == 0 || got[0].Reasons[0].Code != FailureEvaluationMismatch {
		t.Fatalf("attributions = %#v", got)
	}
}

func TestAttributeFailuresUsesDefaultExpectedAgentWithCaseOverride(t *testing.T) {
	result := &engine.EvaluationResult{EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{
		{EvalCaseID: "default", Trace: &atrace.Trace{Steps: []atrace.Step{{AgentName: "wrong"}}}, Metrics: []engine.MetricResult{{MetricName: "quality", Status: status.EvalStatusFailed}}},
		{EvalCaseID: "override", Trace: &atrace.Trace{Steps: []atrace.Step{{AgentName: "special"}}}, Metrics: []engine.MetricResult{{MetricName: "quality", Status: status.EvalStatusFailed}}},
	}}}}
	got := AttributeFailures(result, AttributionOptions{ExpectedAgentName: "candidate", ExpectedAgentNames: map[string]string{"override": "special"}})
	if len(got) != 2 || got[0].Reasons[0].Code != FailureRoutingError {
		t.Fatalf("default expected agent was not applied: %#v", got)
	}
	for _, reason := range got[1].Reasons {
		if reason.Code == FailureRoutingError {
			t.Fatalf("case override was ignored: %#v", got[1])
		}
	}
}

func TestClassifyMetricRecognizesKnowledgeRetrievalNames(t *testing.T) {
	for _, metricName := range []string{"context_retrieve", "knowledge_retrieval"} {
		code, _ := classifyMetric(metricName, "missing context")
		if code != FailureKnowledgeGap {
			t.Errorf("classifyMetric(%q) code = %q, want %q", metricName, code, FailureKnowledgeGap)
		}
	}
}

func message(content string) *model.Message {
	value := model.NewAssistantMessage(content)
	return &value
}
