//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tooltrajectory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestToolTrajectoryEvaluateSuccess(t *testing.T) {
	inst := New()
	assert.NotEmpty(t, inst.Description())
	assert.Equal(t, "tool_trajectory_avg_score", inst.Name())

	e := inst.(*toolTrajectoryEvaluator)
	actual := makeInvocation([]*genai.FunctionCall{
		{Name: "lookup", Args: map[string]any{"id": 1}},
	})
	expected := makeInvocation([]*genai.FunctionCall{
		{Name: "lookup", Args: map[string]any{"id": 1}},
	})

	result, err := e.Evaluate(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, &metric.EvalMetric{Threshold: 0.5})
	assert.NoError(t, err)
	assert.Equal(t, 1.0, result.OverallScore)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, actual, result.PerInvocationResults[0].ActualInvocation)
	assert.Equal(t, expected, result.PerInvocationResults[0].ExpectedInvocation)
	assert.Equal(t, status.EvalStatusPassed, result.PerInvocationResults[0].Status)
}

func TestToolTrajectoryEvaluateMismatch(t *testing.T) {
	e := New().(*toolTrajectoryEvaluator)
	_, err := e.Evaluate(context.Background(), []*evalset.Invocation{}, []*evalset.Invocation{makeInvocation(nil)}, &metric.EvalMetric{Threshold: 1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "count mismatch")
}

func TestToolTrajectoryEvaluateFailureStatus(t *testing.T) {
	e := New().(*toolTrajectoryEvaluator)
	actual := makeInvocation([]*genai.FunctionCall{
		{Name: "lookup", Args: map[string]any{"id": 1}},
	})
	expected := makeInvocation([]*genai.FunctionCall{
		{Name: "lookup", Args: map[string]any{"id": 2}},
	})

	result, err := e.Evaluate(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, &metric.EvalMetric{Threshold: 0.9})
	assert.NoError(t, err)
	assert.Zero(t, result.OverallScore)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Equal(t, status.EvalStatusFailed, result.PerInvocationResults[0].Status)
}

func TestToolTrajectoryEvaluateNotEvaluated(t *testing.T) {
	e := New().(*toolTrajectoryEvaluator)
	result, err := e.Evaluate(context.Background(), []*evalset.Invocation{}, []*evalset.Invocation{}, &metric.EvalMetric{Threshold: 1})
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)
	assert.Nil(t, result.PerInvocationResults)
}

func TestGetToolCallsAndEqual(t *testing.T) {
	assert.Nil(t, getToolCalls(nil))
	assert.Nil(t, getToolCalls(&evalset.Invocation{}))

	callA := []*genai.FunctionCall{{Name: "a", Args: map[string]any{"x": 1}}}
	callB := []*genai.FunctionCall{{Name: "a", Args: map[string]any{"x": 1}}}
	assert.True(t, toolCallsEqual(callA, callB))

	callNameDiff := []*genai.FunctionCall{{Name: "b", Args: map[string]any{"x": 1}}}
	callArgsDiff := []*genai.FunctionCall{{Name: "a", Args: map[string]any{"x": 2}}}
	assert.False(t, toolCallsEqual(callA, callNameDiff))
	assert.False(t, toolCallsEqual(callA, callArgsDiff))
	assert.False(t, toolCallsEqual(callA, []*genai.FunctionCall{}))
}

func makeInvocation(calls []*genai.FunctionCall) *evalset.Invocation {
	return &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: calls,
		},
	}
}
