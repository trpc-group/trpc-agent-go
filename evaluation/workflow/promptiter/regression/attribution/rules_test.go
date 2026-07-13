//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package attribution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestRulesAttributeValidatesRequiredCaseEvidence(t *testing.T) {
	rules := NewRules()
	_, err := rules.Attribute(context.Background(), nil)
	require.ErrorContains(t, err, "case result is nil")

	_, err = rules.Attribute(context.Background(), &regression.CaseResult{})
	require.ErrorContains(t, err, "case id is empty")
}

func TestRulesAttributePrioritizesExecutionEvidence(t *testing.T) {
	tests := []struct {
		name     string
		result   regression.CaseResult
		category regression.FailureCategory
		source   string
	}{
		{
			name: "structured tool error wins",
			result: regression.CaseResult{Runs: []regression.Observation{{
				Tools: []regression.ToolObservation{{Name: "lookup", Error: "permission denied"}},
			}}},
			category: regression.FailureToolResultHandling, source: "tool",
		},
		{
			name: "tool named execution error",
			result: regression.CaseResult{Runs: []regression.Observation{{
				Error: "lookup tool failed", Tools: []regression.ToolObservation{{Name: "lookup"}},
			}}},
			category: regression.FailureToolResultHandling, source: "execution",
		},
		{
			name:     "ordinary execution error",
			result:   regression.CaseResult{Runs: []regression.Observation{{Error: "model unavailable"}}},
			category: regression.FailureInferenceError, source: "execution",
		},
		{
			name: "trace function call error",
			result: regression.CaseResult{Runs: []regression.Observation{{
				Tools: []regression.ToolObservation{{Name: "lookup"}},
				Trace: []regression.TraceStep{{Error: "function call timed out"}},
			}}},
			category: regression.FailureToolResultHandling, source: "trace",
		},
		{
			name: "ordinary trace error",
			result: regression.CaseResult{Runs: []regression.Observation{{
				Trace: []regression.TraceStep{{Error: "route crashed"}},
			}}},
			category: regression.FailureInferenceError, source: "trace",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.result.EvalSetID = "validation"
			test.result.CaseID = "case-1"
			actual, err := NewRules().Attribute(context.Background(), &test.result)
			require.NoError(t, err)
			assert.Equal(t, test.category, actual.Category)
			require.Len(t, actual.Evidence, 1)
			assert.Equal(t, test.source, actual.Evidence[0].Source)
		})
	}
}

func TestRulesAttributeClassifiesMetricsAndUsesStablePriority(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []regression.MetricResult
		category regression.FailureCategory
		reason   string
	}{
		{
			name: "safety has priority over a lexically earlier tool failure",
			metrics: []regression.MetricResult{
				{Name: "tool_selection", Passed: false, Reason: "wrong tool"},
				{Name: "safety", Passed: false, Reason: "private data disclosed"},
			},
			category: regression.FailureSafetyPolicy, reason: "private data disclosed",
		},
		{
			name:     "tool trajectory argument signal",
			metrics:  []regression.MetricResult{{Name: "tool_trajectory_avg_score", Passed: false, Reason: "arguments mismatch"}},
			category: regression.FailureToolArgument, reason: "arguments mismatch",
		},
		{
			name:     "final response format signal",
			metrics:  []regression.MetricResult{{Name: "final_response_avg_score", Passed: false, Reason: "invalid JSON schema"}},
			category: regression.FailureFormat, reason: "invalid JSON schema",
		},
		{
			name:     "rubric category and rubric reason",
			metrics:  []regression.MetricResult{{Name: "llm_rubric_critic", Passed: false, Rubrics: []regression.RubricResult{{ID: "privacy", Reason: "private data exposed"}}}},
			category: regression.FailureSafetyPolicy, reason: "private data exposed",
		},
		{
			name:     "inferred knowledge category",
			metrics:  []regression.MetricResult{{Name: "retrieval_completeness", Passed: false, Reason: "missing source"}},
			category: regression.FailureKnowledgeRecall, reason: "missing source",
		},
		{
			name:     "metric fallback reason",
			metrics:  []regression.MetricResult{{Name: "custom", Passed: false}},
			category: regression.FailureUnknown, reason: "metric \"custom\" failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NewRules().Attribute(context.Background(), &regression.CaseResult{
				EvalSetID: "validation", CaseID: "case-1", Metrics: test.metrics,
			})
			require.NoError(t, err)
			assert.Equal(t, test.category, actual.Category)
			assert.Equal(t, test.reason, actual.Reason)
			assert.Equal(t, "metric", actual.Evidence[0].Source)
		})
	}
}

func TestRulesAttributeFallsBackWithoutFailures(t *testing.T) {
	actual, err := NewRules().Attribute(context.Background(), &regression.CaseResult{
		EvalSetID: "validation", CaseID: "case-1", Metrics: []regression.MetricResult{{Name: "quality", Passed: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, regression.FailureUnknown, actual.Category)
	assert.Equal(t, "case", actual.Evidence[0].Source)
}

func TestClassificationHelpersCoverStableCategories(t *testing.T) {
	for _, test := range []struct {
		name     string
		metric   regression.MetricResult
		category regression.FailureCategory
	}{
		{name: "tool arguments", metric: regression.MetricResult{Name: "tool_arguments"}, category: regression.FailureToolArgument},
		{name: "route", metric: regression.MetricResult{Name: "route"}, category: regression.FailureRoute},
		{name: "format", metric: regression.MetricResult{Name: "format"}, category: regression.FailureFormat},
		{name: "knowledge", metric: regression.MetricResult{Name: "knowledge_recall"}, category: regression.FailureKnowledgeRecall},
		{name: "task success", metric: regression.MetricResult{Name: "task_success"}, category: regression.FailureFinalResponseMismatch},
		{name: "tool result", metric: regression.MetricResult{Name: "tool_trajectory_avg_score", Reason: "tool result mismatch"}, category: regression.FailureToolResultHandling},
		{name: "tool selection", metric: regression.MetricResult{Name: "tool_trajectory_avg_score"}, category: regression.FailureToolSelection},
		{name: "final response", metric: regression.MetricResult{Name: "final_response_avg_score"}, category: regression.FailureFinalResponseMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.category, metricCategory(test.metric))
		})
	}

	for _, test := range []struct {
		name     string
		value    string
		category regression.FailureCategory
	}{
		{name: "tool result", value: "tool result handling failed", category: regression.FailureToolResultHandling},
		{name: "tool selection", value: "tool invocation failed", category: regression.FailureToolSelection},
		{name: "route", value: "router picked the wrong branch", category: regression.FailureRoute},
		{name: "format", value: "structured response invalid", category: regression.FailureFormat},
		{name: "final response", value: "rouge score too low", category: regression.FailureFinalResponseMismatch},
		{name: "unknown", value: "other", category: regression.FailureUnknown},
	} {
		t.Run("inferred "+test.name, func(t *testing.T) {
			assert.Equal(t, test.category, inferredMetricCategory(test.value))
		})
	}

	for _, test := range []struct {
		name     string
		value    string
		category regression.FailureCategory
	}{
		{name: "tool arguments", value: "tool parameter missing", category: regression.FailureToolArgument},
		{name: "tool selection", value: "wrong tool call", category: regression.FailureToolSelection},
		{name: "route", value: "transfer to the wrong sub-agent", category: regression.FailureRoute},
		{name: "format", value: "invalid xml", category: regression.FailureFormat},
		{name: "knowledge", value: "missing source grounding", category: regression.FailureKnowledgeRecall},
		{name: "final response", value: "answer is incomplete", category: regression.FailureFinalResponseMismatch},
	} {
		t.Run("rubric "+test.name, func(t *testing.T) {
			assert.Equal(t, test.category, rubricCategory(test.value))
		})
	}

	assert.False(t, toolExecutionFailure("", []regression.ToolObservation{{Name: "lookup"}}))
	assert.False(t, toolExecutionFailure("lookup failed", nil))
	assert.True(t, toolExecutionFailure("lookup failed", []regression.ToolObservation{{Name: "lookup"}}))
	assert.Equal(t, 8, categoryPriority(regression.FailureUnknown))
	assert.Equal(t, 6, categoryPriority(regression.FailureKnowledgeRecall))
}
