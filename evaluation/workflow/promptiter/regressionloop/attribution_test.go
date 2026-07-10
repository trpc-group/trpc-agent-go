//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAttributeFailuresClassifiesKnownCategories(t *testing.T) {
	result := evalResult("train", []caseSpec{
		{
			id:     "tool_args",
			metric: "tool_trajectory_avg_score",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "tool argument city mismatched",
		},
		{
			id:     "format",
			metric: "json_schema",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "JSON parse failed",
		},
		{
			id:     "final",
			metric: "final_response",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "final response does not match expected answer",
		},
	})
	attrs := AttributeFailures(result)
	assert.Len(t, attrs, 3)
	byCase := map[string]FailureCategory{}
	for _, attr := range attrs {
		byCase[attr.EvalCaseID] = attr.Category
		assert.NotEmpty(t, attr.Reason)
		assert.Equal(t, "deterministic_rules", attr.Method)
		assert.Greater(t, attr.Confidence, 0.0)
	}
	assert.Equal(t, FailureToolArgumentError, byCase["tool_args"])
	assert.Equal(t, FailureFormatError, byCase["format"])
	assert.Equal(t, FailureFinalResponseMismatch, byCase["final"])
}

func TestBuildLossHintsKeepsExplainableReason(t *testing.T) {
	hints := BuildLossHints([]CaseAttribution{
		{
			EvalCaseID: "case",
			MetricName: "metric",
			Category:   FailureRouteError,
			Severity:   "P0",
			Reason:     "wrong route",
			Evidence:   []string{"router=bad"},
		},
	})
	assert.Len(t, hints, 1)
	assert.Equal(t, "case", hints[0].EvalCaseID)
	assert.Contains(t, hints[0].Reason, "failure_category=route_error")
	assert.Contains(t, hints[0].Reason, "router=bad")
}

func TestAttributeFailuresClassifiesAdditionalSignals(t *testing.T) {
	result := evalResult("train", []caseSpec{
		{id: "route", metric: "router_decision", score: 0, status: status.EvalStatusFailed, reason: "route selected wrong agent"},
		{id: "knowledge", metric: "knowledge_recall", score: 0, status: status.EvalStatusFailed, reason: "knowledge recall missing context"},
		{id: "rubric", metric: "llm_rubric", score: 0, status: status.EvalStatusFailed, reason: "rubric judge rejected quality"},
		{id: "generic_final", metric: "business_exactness", score: 0, status: status.EvalStatusFailed, reason: "expected approved but actual response said pending"},
		{id: "generic_tool", metric: "business_flow", score: 0, status: status.EvalStatusFailed, reason: "did not call billing_lookup before answering"},
		{id: "opaque", metric: "custom_quality", score: 0, status: status.EvalStatusFailed, reason: "opaque judge failed"},
	})
	attrs := AttributeFailures(result)
	byCase := map[string]FailureCategory{}
	for _, attr := range attrs {
		byCase[attr.EvalCaseID] = attr.Category
	}
	assert.Equal(t, FailureRouteError, byCase["route"])
	assert.Equal(t, FailureKnowledgeRecallGap, byCase["knowledge"])
	assert.Equal(t, FailureRubricFailure, byCase["rubric"])
	assert.Equal(t, FailureFinalResponseMismatch, byCase["generic_final"])
	assert.Equal(t, FailureToolCallError, byCase["generic_tool"])
	assert.Equal(t, FailureRubricFailure, byCase["opaque"])
}

func TestAttributeFailuresUsesConfiguredMetricHints(t *testing.T) {
	result := evalResult("train", []caseSpec{
		{
			id:     "custom",
			metric: "business_policy_score",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "opaque judge failed",
		},
	})
	attrs := AttributeFailuresWithHints(result, map[string]FailureCategory{
		"business_policy_score": FailureKnowledgeRecallGap,
	})
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureKnowledgeRecallGap, attrs[0].Category)
	assert.Equal(t, "configured_hint", attrs[0].Method)
	assert.Equal(t, 0.95, attrs[0].Confidence)
}

func TestStructuredDiffOverridesConfiguredMetricHint(t *testing.T) {
	result := structuredEvalResult("validation", []promptiterengine.CaseResult{
		{
			EvalCaseID: "case",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "search_orders", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			ExpectedInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "business_policy_score", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
	})
	attrs := AttributeFailuresWithHints(result, map[string]FailureCategory{
		"business_policy_score": FailureKnowledgeRecallGap,
	})
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureToolCallError, attrs[0].Category)
	assert.Equal(t, "structured_diff", attrs[0].Method)
	assert.Contains(t, attrs[0].Evidence, "configured_hint=knowledge_recall_gap overridden_by=structured_diff")
	assert.Contains(t, attrs[0].SecondaryCategories, FailureKnowledgeRecallGap)
}

func TestAttributeFailuresAddsSecondaryCategories(t *testing.T) {
	result := evalResult("validation", []caseSpec{
		{
			id:     "mixed",
			metric: "payload_contract_score",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "missing tool call and required JSON field missing",
		},
	})
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, []MetricDefinition{
		{MetricName: "payload_contract_score", Criterion: map[string]json.RawMessage{
			"contract": json.RawMessage(`{"description":"response must satisfy json_schema structured output"}`),
		}},
	})
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureToolCallError, attrs[0].Category)
	assert.Contains(t, attrs[0].SecondaryCategories, FailureFormatError)
	assert.Contains(t, attrs[0].Evidence, "metric_definition_hint=format_error superseded_by_reason_signal")

	summary := SummarizeAttributions(attrs)
	assert.Equal(t, 1, summary.ByCategory[FailureToolCallError])
	assert.Equal(t, 1, summary.BySecondaryCategory[FailureFormatError])
}

func TestAttributeFailuresUsesOptionalJudgeFallback(t *testing.T) {
	result := evalResult("validation", []caseSpec{
		{id: "ambiguous", metric: "opaque_quality", score: 0, status: status.EvalStatusFailed, reason: "failed"},
	})
	attrs := AttributeFailuresWithOptions(context.Background(), result, AttributionOptions{
		Judge: fakeAttributionJudge{
			category:    FailureKnowledgeRecallGap,
			confidence:  0.91,
			reason:      "judge saw unsupported policy fact",
			evidence:    []string{"missing citation"},
			secondaries: []FailureCategory{FailureFinalResponseMismatch},
		},
	})
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureKnowledgeRecallGap, attrs[0].Category)
	assert.Equal(t, "judge_fallback", attrs[0].Method)
	assert.Equal(t, 0.91, attrs[0].Confidence)
	assert.Contains(t, attrs[0].SecondaryCategories, FailureRubricFailure)
	assert.Contains(t, attrs[0].SecondaryCategories, FailureFinalResponseMismatch)
	assert.Contains(t, attrs[0].Evidence, "judge_reason=judge saw unsupported policy fact")
	assert.Contains(t, attrs[0].Evidence, "judge_evidence=missing citation")
}

func TestAttributionHintsMergesMetricsAndConfig(t *testing.T) {
	hints := AttributionHints(
		Config{
			Attribution: AttributionConfig{
				MetricCategoryHints: map[string]FailureCategory{
					"metric_from_config": FailureRouteError,
					"metric_from_file":   FailureFormatError,
				},
			},
		},
		[]MetricDefinition{
			{MetricName: "metric_from_file", FailureCategory: FailureToolCallError},
			{MetricName: "metric_only_file", FailureCategory: FailureKnowledgeRecallGap},
		},
	)
	assert.Equal(t, FailureFormatError, hints["metric_from_file"])
	assert.Equal(t, FailureRouteError, hints["metric_from_config"])
	assert.Equal(t, FailureKnowledgeRecallGap, hints["metric_only_file"])
}

func TestAttributeFailuresAddsInferenceErrorFromFailedTrace(t *testing.T) {
	result := &promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{
					{
						EvalCaseID: "case",
						Trace: &atrace.Trace{
							Status: atrace.TraceStatusFailed,
							Steps:  []atrace.Step{{StepID: "step", Error: "tool timeout"}},
						},
					},
				},
			},
		},
	}
	attrs := AttributeFailures(result)
	assert.Len(t, attrs, 1)
	assert.Equal(t, FailureInferenceError, attrs[0].Category)
	assert.Contains(t, attrs[0].Evidence[1], "tool timeout")
}

func TestAttributionCoversTraceAndFallbackBranches(t *testing.T) {
	assert.Nil(t, AttributeFailuresWithHints(nil, nil))
	assert.Nil(t, AttributionHints(Config{}, nil))
	assert.Nil(t, normalizeAttributionHints(map[string]FailureCategory{
		"":      FailureRouteError,
		"empty": "",
		"bad":   "bad",
	}))
	assert.Equal(t, map[string]FailureCategory{
		"valid": FailureRouteError,
	}, AttributionHints(
		Config{Attribution: AttributionConfig{MetricCategoryHints: map[string]FailureCategory{
			"":         FailureRouteError,
			"bad":      "bad",
			"valid":    FailureRouteError,
			"blankCat": "",
		}}},
		nil,
	))
	assert.Equal(t, promptiterengine.LossHint{}, firstOrZeroLossHint(BuildLossHints([]CaseAttribution{
		{EvalCaseID: "", MetricName: "metric"},
	})))
	assert.Equal(t, promptiter.LossSeverityP2, parseSeverity("bad"))

	trace := &atrace.Trace{
		Status: atrace.TraceStatusFailed,
		Steps: []atrace.Step{
			{StepID: "empty"},
			{StepID: "err", Error: "step failed"},
			{StepID: "out", Output: &atrace.Snapshot{Text: "final output"}},
		},
	}
	assert.Contains(t, traceErrorText(trace), "failed")
	assert.Contains(t, traceErrorText(trace), "step failed")
	assert.Equal(t, "final output", finalTraceText(trace))
	assert.True(t, traceHasError(trace))
	assert.False(t, traceHasError(&atrace.Trace{Status: atrace.TraceStatusCompleted}))
	assert.True(t, traceHasError(&atrace.Trace{Steps: []atrace.Step{{Error: "step failed"}}}))
	assert.Empty(t, finalTraceText(nil))
	assert.Empty(t, finalTraceText(&atrace.Trace{Steps: []atrace.Step{{Output: &atrace.Snapshot{}}}}))
	assert.NotContains(t, traceEvidence(&atrace.Trace{Status: atrace.TraceStatusCompleted}), "final_output")
	assert.Contains(t, traceEvidence(&atrace.Trace{
		Status: atrace.TraceStatusCompleted,
		Steps:  []atrace.Step{{Output: &atrace.Snapshot{Text: "final output"}}},
	}), "final_output=final output")

	category, confidence, method := classifyFailure("custom_metric", "", trace, nil, nil, nil, nil)
	assert.Equal(t, FailureInferenceError, category)
	assert.Equal(t, 0.9, confidence)
	assert.Equal(t, "deterministic_rules", method)

	category, confidence, method = classifyFailure("custom_metric", "", nil, nil, nil, nil, nil)
	assert.Equal(t, FailureUnknown, category)
	assert.Equal(t, 0.2, confidence)
	assert.Equal(t, "deterministic_rules", method)

	for _, tt := range []struct {
		metric string
		reason string
		want   FailureCategory
	}{
		{metric: "final_response", reason: "", want: FailureFinalResponseMismatch},
		{metric: "custom", reason: "timeout while running", want: FailureInferenceError},
		{metric: "custom", reason: "rubric score below threshold", want: FailureRubricFailure},
		{metric: "knowledge_recall", reason: "opaque", want: FailureKnowledgeRecallGap},
		{metric: "tool_argument_score", reason: "opaque", want: FailureToolArgumentError},
	} {
		got, _, _ := classifyFailure(tt.metric, tt.reason, nil, nil, nil, nil, nil)
		assert.Equal(t, tt.want, got, tt)
	}
}

func TestMetricDefinitionHintsClassifyOpaqueMetricFailures(t *testing.T) {
	metrics := []MetricDefinition{
		{MetricName: "opaque_case", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}},
		{MetricName: "opaque_tool", Criterion: map[string]json.RawMessage{"toolTrajectory": json.RawMessage(`{}`)}},
		{MetricName: "opaque_route", Criterion: map[string]json.RawMessage{"routerDecision": json.RawMessage(`{}`)}},
		{MetricName: "opaque_tool_raw", Criterion: map[string]json.RawMessage{"quality": json.RawMessage(`{"description":"must use function_call with the right tool name"}`)}},
		{MetricName: "opaque_args_raw", Criterion: map[string]json.RawMessage{"quality": json.RawMessage(`{"description":"compare tool_arguments and function arguments"}`)}},
		{MetricName: "opaque_format_raw", Criterion: map[string]json.RawMessage{"quality": json.RawMessage(`{"description":"validate json_schema structured output"}`)}},
		{MetricName: "opaque_knowledge_raw", Criterion: map[string]json.RawMessage{"quality": json.RawMessage(`{"description":"check grounding, citation, retrieval and factual support"}`)}},
		{MetricName: "opaque_rubric", EvaluatorName: "llm_rubric"},
		{MetricName: "ignored", EvaluatorName: "unknown"},
	}
	hints := MetricDefinitionHints(metrics)
	assert.Equal(t, FailureFormatError, hints["opaque_case"])
	assert.Equal(t, FailureToolCallError, hints["opaque_tool"])
	assert.Equal(t, FailureRouteError, hints["opaque_route"])
	assert.Equal(t, FailureToolCallError, hints["opaque_tool_raw"])
	assert.Equal(t, FailureToolArgumentError, hints["opaque_args_raw"])
	assert.Equal(t, FailureFormatError, hints["opaque_format_raw"])
	assert.Equal(t, FailureKnowledgeRecallGap, hints["opaque_knowledge_raw"])
	assert.Equal(t, FailureRubricFailure, hints["opaque_rubric"])
	assert.NotContains(t, hints, "ignored")

	result := evalResult("validation", []caseSpec{
		{id: "case", metric: "opaque_case", score: 0, status: status.EvalStatusFailed, reason: "failed"},
	})
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, metrics)
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureFormatError, attrs[0].Category)
	assert.Equal(t, "metric_definition_hint", attrs[0].Method)
}

func TestAttributionGoldSetAccuracy(t *testing.T) {
	metrics := []MetricDefinition{
		{MetricName: "opaque_format", Criterion: map[string]json.RawMessage{"contract": json.RawMessage(`{"description":"validate json_schema structured output"}`)}},
		{MetricName: "opaque_route", Criterion: map[string]json.RawMessage{"routing": json.RawMessage(`{"description":"router handoff and agent selection"}`)}},
		{MetricName: "opaque_knowledge", Criterion: map[string]json.RawMessage{"grounded": json.RawMessage(`{"description":"retrieval grounding citation factual support"}`)}},
	}
	result := structuredEvalResult("hidden_like", []promptiterengine.CaseResult{
		{
			EvalCaseID: "final_response",
			ActualInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage("refund cutoff is July"),
			},
			ExpectedInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage("refund cutoff is August"),
			},
			Metrics: []promptiterengine.MetricResult{{MetricName: "final_response", Status: status.EvalStatusFailed, Reason: "failed"}},
		},
		{
			EvalCaseID: "tool_call",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "search_orders"}},
			},
			ExpectedInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup"}},
			},
			Metrics: []promptiterengine.MetricResult{{MetricName: "tool_trajectory", Status: status.EvalStatusFailed, Reason: "failed"}},
		},
		{
			EvalCaseID: "tool_args",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-2"}}},
			},
			ExpectedInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			Metrics: []promptiterengine.MetricResult{{MetricName: "tool_trajectory", Status: status.EvalStatusFailed, Reason: "failed"}},
		},
		{
			EvalCaseID: "route",
			Trace: &atrace.Trace{
				RootAgentName: "general_support",
				Steps:         []atrace.Step{{AgentName: "general_support", NodeID: "general"}},
			},
			ExpectedInvocation: &evalset.Invocation{
				ExecutionTrace: &atrace.Trace{
					RootAgentName: "billing_agent",
					Steps:         []atrace.Step{{AgentName: "billing_agent", NodeID: "billing"}},
				},
			},
			Metrics: []promptiterengine.MetricResult{{MetricName: "opaque_route", Status: status.EvalStatusFailed, Reason: "score below threshold"}},
		},
		{
			EvalCaseID: "format",
			ActualInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage(`{"status":"ok"}`),
			},
			ExpectedInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage(`{"status":"ok","id":"123"}`),
			},
			Metrics: []promptiterengine.MetricResult{{MetricName: "opaque_format", Status: status.EvalStatusFailed, Reason: "failed"}},
		},
		{
			EvalCaseID: "knowledge",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "opaque_knowledge", Status: status.EvalStatusFailed,
				Reason: "引用来源不足 and factually incorrect policy date",
			}},
		},
		{
			EvalCaseID: "rubric",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "llm_rubric", Status: status.EvalStatusFailed,
				Reason: "rubric judge rejected overall answer quality",
			}},
		},
		{
			EvalCaseID: "inference",
			Trace: &atrace.Trace{
				Status: atrace.TraceStatusFailed,
				Steps:  []atrace.Step{{StepID: "run", Error: "deadline exceeded"}},
			},
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "custom_quality", Status: status.EvalStatusFailed,
				Reason: "timeout while running",
			}},
		},
		{
			EvalCaseID: "chinese_route",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "assignment_score", Status: status.EvalStatusFailed,
				Reason: "子代理选择错误",
			}},
		},
		{
			EvalCaseID: "chinese_format",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "payload_score", Status: status.EvalStatusFailed,
				Reason: "结构化字段缺失",
			}},
		},
		{
			EvalCaseID: "business_tool",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "business_flow", Status: status.EvalStatusFailed,
				Reason: "did not invoke billing_lookup before answering",
			}},
		},
		{
			EvalCaseID: "business_final",
			Metrics: []promptiterengine.MetricResult{{
				MetricName: "business_exactness", Status: status.EvalStatusFailed,
				Reason: "expected approved but actual response said pending",
			}},
		},
	})
	want := map[string]FailureCategory{
		"final_response": FailureFinalResponseMismatch,
		"tool_call":      FailureToolCallError,
		"tool_args":      FailureToolArgumentError,
		"route":          FailureRouteError,
		"format":         FailureFormatError,
		"knowledge":      FailureKnowledgeRecallGap,
		"rubric":         FailureRubricFailure,
		"inference":      FailureInferenceError,
		"chinese_route":  FailureRouteError,
		"chinese_format": FailureFormatError,
		"business_tool":  FailureToolCallError,
		"business_final": FailureFinalResponseMismatch,
	}
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, metrics)
	byCase := map[string]FailureCategory{}
	for _, attr := range attrs {
		if _, exists := byCase[attr.EvalCaseID]; !exists || attr.MetricName != "inference" {
			byCase[attr.EvalCaseID] = attr.Category
		}
	}
	correct := 0
	for caseID, expected := range want {
		if assert.Contains(t, byCase, caseID) && byCase[caseID] == expected {
			correct++
		}
	}
	accuracy := float64(correct) / float64(len(want))
	assert.GreaterOrEqual(t, accuracy, 0.85)
}

func TestGenericReasonUsesMetricDefinitionBeforeRubricFallback(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metric   MetricDefinition
		reason   string
		expected FailureCategory
	}{
		{
			name:     "final",
			metric:   MetricDefinition{MetricName: "opaque_a", Criterion: map[string]json.RawMessage{"finalResponse": json.RawMessage(`{}`)}},
			reason:   "score below threshold",
			expected: FailureFinalResponseMismatch,
		},
		{
			name:     "format",
			metric:   MetricDefinition{MetricName: "opaque_b", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}},
			reason:   "score below threshold",
			expected: FailureFormatError,
		},
		{
			name:     "route",
			metric:   MetricDefinition{MetricName: "opaque_c", Criterion: map[string]json.RawMessage{"routerDecision": json.RawMessage(`{}`)}},
			reason:   "score below threshold",
			expected: FailureRouteError,
		},
		{
			name:     "chinese_format",
			metric:   MetricDefinition{MetricName: "opaque_d", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}},
			reason:   "低于阈值",
			expected: FailureFormatError,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := evalResult("validation", []caseSpec{
				{id: "case", metric: tt.metric.MetricName, score: 0, status: status.EvalStatusFailed, reason: tt.reason},
			})
			attrs := AttributeFailuresWithMetricDefinitions(result, nil, []MetricDefinition{tt.metric})
			require.Len(t, attrs, 1)
			assert.Equal(t, tt.expected, attrs[0].Category)
			assert.Equal(t, "metric_definition_hint", attrs[0].Method)
		})
	}
}

func TestAttributeFailuresClassifiesBusinessMetricsWithHiddenSamplePhrases(t *testing.T) {
	metrics := []MetricDefinition{
		{MetricName: "checkout_flow_quality", Criterion: map[string]json.RawMessage{"toolCalls": json.RawMessage(`{}`)}},
		{MetricName: "case_assignment_accuracy", Criterion: map[string]json.RawMessage{"routerDecision": json.RawMessage(`{}`)}},
		{MetricName: "payload_contract_score", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}},
		{MetricName: "answer_grounding_score", Criterion: map[string]json.RawMessage{"finalResponse": json.RawMessage(`{}`)}},
	}
	result := evalResult("validation", []caseSpec{
		{
			id:     "tool",
			metric: "checkout_flow_quality",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "未调用 billing_lookup 工具 before answering",
		},
		{
			id:     "route",
			metric: "case_assignment_accuracy",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "子代理选择错误: selected the wrong agent",
		},
		{
			id:     "format",
			metric: "payload_contract_score",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "结构化字段缺失: required field status missing",
		},
		{
			id:     "knowledge",
			metric: "answer_grounding_score",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "引用来源不足 and factually incorrect policy date",
		},
	})
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, metrics)
	byCase := map[string]FailureCategory{}
	for _, attr := range attrs {
		byCase[attr.EvalCaseID] = attr.Category
	}
	assert.Equal(t, FailureToolCallError, byCase["tool"])
	assert.Equal(t, FailureRouteError, byCase["route"])
	assert.Equal(t, FailureFormatError, byCase["format"])
	assert.Equal(t, FailureKnowledgeRecallGap, byCase["knowledge"])
}

func TestSemanticReasonOverridesMetricDefinitionHint(t *testing.T) {
	result := evalResult("validation", []caseSpec{
		{
			id:     "case",
			metric: "opaque_json",
			score:  0,
			status: status.EvalStatusFailed,
			reason: "missing tool call before answering",
		},
	})
	attrs := AttributeFailuresWithMetricDefinitions(
		result,
		nil,
		[]MetricDefinition{{MetricName: "opaque_json", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}}},
	)
	require.Len(t, attrs, 1)
	assert.Equal(t, FailureToolCallError, attrs[0].Category)
	assert.Equal(t, "deterministic_rules", attrs[0].Method)
}

func TestAttributeFailuresSortsAndSynthesizesEmptyReasons(t *testing.T) {
	result := &promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID: "b",
				Cases: []promptiterengine.CaseResult{
					{
						EvalCaseID: "case",
						Metrics: []promptiterengine.MetricResult{
							{MetricName: "z", Status: status.EvalStatusFailed},
						},
					},
				},
			},
			{
				EvalSetID: "a",
				Cases: []promptiterengine.CaseResult{
					{
						EvalCaseID: "case",
						Metrics: []promptiterengine.MetricResult{
							{MetricName: "z", Status: status.EvalStatusFailed},
							{MetricName: "a", Status: status.EvalStatusFailed},
						},
					},
				},
			},
		},
	}
	attrs := AttributeFailures(result)
	require.Len(t, attrs, 3)
	assert.Equal(t, "a", attrs[0].EvalSetID)
	assert.Equal(t, "a", attrs[0].MetricName)
	assert.Contains(t, attrs[0].Reason, "failed without a detailed reason")
}

func TestAttributeFailuresUsesStructuredToolDiff(t *testing.T) {
	result := structuredEvalResult("validation", []promptiterengine.CaseResult{
		{
			EvalCaseID: "wrong_tool",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "search_orders", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			ExpectedInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "business_flow", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
		{
			EvalCaseID: "unexpected_tool",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			ExpectedInvocation: &evalset.Invocation{},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "tool_trajectory", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
		{
			EvalCaseID: "bad_args",
			ActualInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-2"}}},
			},
			ExpectedInvocation: &evalset.Invocation{
				Tools: []*evalset.Tool{{Name: "billing_lookup", Arguments: map[string]any{"invoice_id": "A-1"}}},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "tool_trajectory", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
	})
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, []MetricDefinition{
		{MetricName: "business_flow", Criterion: map[string]json.RawMessage{"toolTrajectory": json.RawMessage(`{}`)}},
		{MetricName: "tool_trajectory", Criterion: map[string]json.RawMessage{"toolTrajectory": json.RawMessage(`{}`)}},
	})
	require.Len(t, attrs, 3)
	byCase := map[string]CaseAttribution{}
	for _, attr := range attrs {
		byCase[attr.EvalCaseID] = attr
		assert.Equal(t, "structured_diff", attr.Method)
		if attr.EvalCaseID != "unexpected_tool" {
			assert.Contains(t, attr.Evidence, "expected_tools=billing_lookup")
		}
	}
	assert.Equal(t, FailureToolCallError, byCase["wrong_tool"].Category)
	assert.Equal(t, FailureToolCallError, byCase["unexpected_tool"].Category)
	assert.Equal(t, FailureToolArgumentError, byCase["bad_args"].Category)
}

func TestAttributeFailuresUsesStructuredFormatRouteAndFinalDiff(t *testing.T) {
	result := structuredEvalResult("validation", []promptiterengine.CaseResult{
		{
			EvalCaseID: "json_missing_field",
			ActualInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage(`{"status":"ok"}`),
			},
			ExpectedInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage(`{"status":"ok","id":"123"}`),
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "payload_score", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
		{
			EvalCaseID: "route_wrong",
			Trace: &atrace.Trace{
				RootAgentName: "general_support",
				Steps:         []atrace.Step{{AgentName: "general_support", NodeID: "general"}},
			},
			ExpectedInvocation: &evalset.Invocation{
				ExecutionTrace: &atrace.Trace{
					RootAgentName: "billing_agent",
					Steps:         []atrace.Step{{AgentName: "billing_agent", NodeID: "billing"}},
				},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "assignment_score", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
		{
			EvalCaseID: "route_wrong_empty_reason",
			Trace: &atrace.Trace{
				RootAgentName: "general_support",
				Steps:         []atrace.Step{{AgentName: "general_support", NodeID: "general"}},
			},
			ExpectedInvocation: &evalset.Invocation{
				ExecutionTrace: &atrace.Trace{
					RootAgentName: "billing_agent",
					Steps:         []atrace.Step{{AgentName: "billing_agent", NodeID: "billing"}},
				},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "assignment_score", Status: status.EvalStatusFailed},
			},
		},
		{
			EvalCaseID: "answer_mismatch",
			ActualInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage("refund cutoff is 2026-07-31"),
			},
			ExpectedInvocation: &evalset.Invocation{
				FinalResponse: assistantMessage("refund cutoff is 2026-08-31"),
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "exact_answer", Status: status.EvalStatusFailed, Reason: "failed"},
			},
		},
	})
	attrs := AttributeFailuresWithMetricDefinitions(result, nil, []MetricDefinition{
		{MetricName: "payload_score", Criterion: map[string]json.RawMessage{"json": json.RawMessage(`{}`)}},
		{MetricName: "assignment_score", Criterion: map[string]json.RawMessage{"routerDecision": json.RawMessage(`{}`)}},
		{MetricName: "exact_answer", Criterion: map[string]json.RawMessage{"finalResponse": json.RawMessage(`{}`)}},
	})
	require.Len(t, attrs, 4)
	byCase := map[string]CaseAttribution{}
	for _, attr := range attrs {
		byCase[attr.EvalCaseID] = attr
		assert.Equal(t, "structured_diff", attr.Method)
	}
	assert.Equal(t, FailureFormatError, byCase["json_missing_field"].Category)
	assert.Equal(t, FailureRouteError, byCase["route_wrong"].Category)
	assert.Equal(t, FailureRouteError, byCase["route_wrong_empty_reason"].Category)
	assert.Equal(t, FailureFinalResponseMismatch, byCase["answer_mismatch"].Category)
}

func structuredEvalResult(evalSetID string, cases []promptiterengine.CaseResult) *promptiterengine.EvaluationResult {
	for i := range cases {
		cases[i].EvalSetID = evalSetID
	}
	return &promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{EvalSetID: evalSetID, Cases: cases},
		},
	}
}

func assistantMessage(content string) *model.Message {
	message := model.NewAssistantMessage(content)
	return &message
}

func firstOrZeroLossHint(hints []promptiterengine.LossHint) promptiterengine.LossHint {
	if len(hints) == 0 {
		return promptiterengine.LossHint{}
	}
	return hints[0]
}

type fakeAttributionJudge struct {
	category    FailureCategory
	confidence  float64
	reason      string
	evidence    []string
	secondaries []FailureCategory
}

func (j fakeAttributionJudge) ClassifyFailure(context.Context, AttributionJudgeRequest) (AttributionJudgeResult, error) {
	return AttributionJudgeResult{
		Category:    j.category,
		Confidence:  j.confidence,
		Reason:      j.reason,
		Evidence:    j.evidence,
		Secondaries: j.secondaries,
	}, nil
}
