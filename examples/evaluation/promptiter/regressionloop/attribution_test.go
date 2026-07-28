//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAttributeUsesMetricScopedToolEvidence(t *testing.T) {
	evalCase := failedCase("tool_trajectory", "tool output mismatch")
	evalCase.MetricEvidence = []*evalresult.EvalMetricResultPerInvocation{
		{
			ActualInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
				Name: "search", Arguments: map[string]any{"q": "same"},
			}}},
			ExpectedInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
				Name: "search", Arguments: map[string]any{"q": "same"},
			}}},
			EvalMetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "tool_trajectory", EvalStatus: status.EvalStatusFailed,
			}},
		},
		{
			ActualInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
				Name: "search", Arguments: map[string]any{"q": "wrong"},
			}}},
			ExpectedInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
				Name: "search", Arguments: map[string]any{"q": "right"},
			}}},
			EvalMetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "unrelated_metric", EvalStatus: status.EvalStatusFailed,
			}},
		},
	}

	got := attributeCase(evalCase)

	assert.Equal(t, attributionToolCallError, got.Primary.Category)
}

func TestAttributeIncompleteEvaluation(t *testing.T) {
	evalCase := failedCase("quality", "")
	evalCase.Metrics[0].Status = status.EvalStatusNotEvaluated

	assert.Equal(t, attributionEvaluationIncomplete, attributeCase(evalCase).Primary.Category)
}

func TestAttributeHonorsPrimaryPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*caseResult)
		expected attributionCategory
	}{
		{
			name: "runtime before metric",
			mutate: func(evalCase *caseResult) {
				evalCase.ExecutionError = "provider timeout"
			},
			expected: attributionRuntimeError,
		},
		{
			name: "tool arguments",
			mutate: func(evalCase *caseResult) {
				evalCase.Metrics[0].Name = "tool_trajectory_avg_score"
				evalCase.MetricEvidence = []*evalresult.EvalMetricResultPerInvocation{{
					ActualInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
						Name: "search", Arguments: map[string]any{"q": "wrong"},
					}}},
					ExpectedInvocation: &evalset.Invocation{Tools: []*evalset.Tool{{
						Name: "search", Arguments: map[string]any{"q": "right"},
					}}},
					EvalMetricResults: []*evalresult.EvalMetricResult{{
						MetricName: "tool_trajectory_avg_score",
						EvalStatus: status.EvalStatusFailed,
					}},
				}}
			},
			expected: attributionToolParameterError,
		},
		{
			name: "format",
			mutate: func(evalCase *caseResult) {
				evalCase.Metrics[0].Name = "json_format"
			},
			expected: attributionFormatError,
		},
		{
			name: "unclassified",
			mutate: func(evalCase *caseResult) {
				evalCase.Metrics[0].Name = "custom"
			},
			expected: attributionUnclassifiedFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evalCase := failedCase("final_response", "failure")
			tt.mutate(&evalCase)
			assert.Equal(t, tt.expected, attributeCase(evalCase).Primary.Category)
		})
	}
}

func failedCase(metricName, reason string) caseResult {
	return caseResult{
		EvalSetID:  "validation",
		EvalCaseID: "critical",
		Status:     status.EvalStatusFailed,
		Metrics: []metricResult{{
			Name:   metricName,
			Status: status.EvalStatusFailed,
			Reason: reason,
		}},
	}
}
