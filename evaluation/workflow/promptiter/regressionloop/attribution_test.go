// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestAttributionRouteError(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_route_error",
						Metrics: []engine.MetricResult{
							{
								MetricName: "router_metric",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "routing failed to find correct handler",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionRouteError, attributions[0].Category)
	assert.Equal(t, "case_route_error", attributions[0].EvalCaseID)
	assert.Equal(t, "router_metric", attributions[0].MetricName)
}

func TestAttributionToolCallError(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_tool_call_error",
						Metrics: []engine.MetricResult{
							{
								MetricName: "tool_call_metric",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "tool call failed: unknown tool name",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionToolCallError, attributions[0].Category)
}

func TestAttributionToolArgumentError(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_tool_argument_error",
						Metrics: []engine.MetricResult{
							{
								MetricName: "parameter_validator",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "invalid argument type for parameter 'id'",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionToolArgumentError, attributions[0].Category)
}

func TestAttributionFormatError(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_format_error",
						Metrics: []engine.MetricResult{
							{
								MetricName: "format_validator",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "JSON parse error: unexpected token",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionFormatError, attributions[0].Category)
}

func TestAttributionKnowledgeRecallGap(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_knowledge_gap",
						Metrics: []engine.MetricResult{
							{
								MetricName: "knowledge_recall_score",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "missing_information from knowledge base",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionKnowledgeRecallGap, attributions[0].Category)
}

func TestAttributionResponseMismatch(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_response_mismatch",
						Metrics: []engine.MetricResult{
							{
								MetricName: "final_response",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "response does not match expected output",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 1)
	assert.Equal(t, AttributionResponseMismatch, attributions[0].Category)
}

func TestAttributionCausalChainFolding(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_multi_errors",
						Metrics: []engine.MetricResult{
							{
								MetricName: "router_metric",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "routing failed",
							},
							{
								MetricName: "tool_call_error",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "tool call failed",
							},
							{
								MetricName: "final_response",
								Score:      0.0,
								Status:     status.EvalStatusFailed,
								Reason:     "response mismatch",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 3)

	for _, attr := range attributions {
		if attr.Category == AttributionRouteError {
			assert.Contains(t, attr.DerivedFrom, "tool_call_error")
			assert.Contains(t, attr.DerivedFrom, "response_mismatch")
		}
	}
}

func TestAttributionPassedCases(t *testing.T) {
	result := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case_passed",
						Metrics: []engine.MetricResult{
							{
								MetricName: "test_metric",
								Score:      1.0,
								Status:     status.EvalStatusPassed,
								Reason:     "",
							},
						},
					},
				},
			},
		},
	}

	attributions := AttributeFailures(result, nil)
	assert.Len(t, attributions, 0)
}

func TestGetAttributionSummary(t *testing.T) {
	attributions := []AttributionResult{
		{Category: AttributionRouteError},
		{Category: AttributionRouteError},
		{Category: AttributionToolCallError},
		{Category: AttributionFormatError},
	}

	summary := GetAttributionSummary(attributions)
	assert.Equal(t, 2, summary["route_error"])
	assert.Equal(t, 1, summary["tool_call_error"])
	assert.Equal(t, 1, summary["format_error"])
	assert.Equal(t, 0, summary["response_mismatch"])
}

func TestSeverityFromCategory(t *testing.T) {
	assert.Equal(t, "P0", string(severityFromCategory(AttributionRouteError)))
	assert.Equal(t, "P0", string(severityFromCategory(AttributionToolCallError)))
	assert.Equal(t, "P1", string(severityFromCategory(AttributionToolArgumentError)))
	assert.Equal(t, "P1", string(severityFromCategory(AttributionFormatError)))
	assert.Equal(t, "P2", string(severityFromCategory(AttributionKnowledgeRecallGap)))
	assert.Equal(t, "P3", string(severityFromCategory(AttributionResponseMismatch)))
}
