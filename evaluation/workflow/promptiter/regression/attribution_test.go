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

func TestAttributeFailuresAlwaysExplainsFailedCase(t *testing.T) {
	result := &engine.EvaluationResult{EvalSets: []engine.EvalSetResult{{Cases: []engine.CaseResult{{EvalCaseID: "case", Metrics: []engine.MetricResult{{MetricName: "custom", Status: status.EvalStatusFailed}}}}}}}
	got := AttributeFailures(result, AttributionOptions{})
	if len(got) != 1 || len(got[0].Reasons) == 0 || got[0].Reasons[0].Code != FailureEvaluationMismatch {
		t.Fatalf("attributions = %#v", got)
	}
}

func message(content string) *model.Message {
	value := model.NewAssistantMessage(content)
	return &value
}
