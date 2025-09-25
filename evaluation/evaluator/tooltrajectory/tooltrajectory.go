//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tooltrajectory provides tool trajectory-based evaluation.
package tooltrajectory

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const (
	defaultMetricName = "tool_trajectory_avg_score"
	defaultThreshold  = 1.0
)

// toolTrajectoryEvaluator is a tool trajectory evaluator implementation for evaluator.
type toolTrajectoryEvaluator struct {
	evalMetric *metric.EvalMetric
}

// New creates a new trajectory evaluator.
func New(opt ...evaluator.Option) evaluator.Evaluator {
	opts := &evaluator.Options{
		EvalMetric: &metric.EvalMetric{
			MetricName: defaultMetricName,
			Threshold:  defaultThreshold,
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return &toolTrajectoryEvaluator{evalMetric: opts.EvalMetric}
}

// Name returns the name of this evaluator.
func (e *toolTrajectoryEvaluator) Name() string {
	return e.evalMetric.MetricName
}

// Description returns a description of what this evaluator does.
func (e *toolTrajectoryEvaluator) Description() string {
	return "Evaluates the accuracy of tool usage trajectory including sequence and arguments"
}

// Evaluate compares tool usage trajectories between actual and expected invocations.
func (e *toolTrajectoryEvaluator) Evaluate(ctx context.Context,
	actuals, expecteds []*evalset.Invocation) (*evaluator.EvaluationResult, error) {
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("tooltrajectory: actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	perInvocation := make([]evaluator.PerInvocationResult, 0, len(actuals))
	var totalScore float64
	for i := 0; i < len(actuals); i++ {
		actual := actuals[i]
		expected := expecteds[i]
		actualCalls := getToolCalls(actual)
		expectedCalls := getToolCalls(expected)
		score := 0.0
		if toolCallsEqual(actualCalls, expectedCalls) {
			score = 1.0
		}
		status := e.statusForScore(score)
		perInvocation = append(perInvocation, evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expected,
			Score:              score,
			Status:             status,
		})
		totalScore += score
	}
	if len(perInvocation) == 0 {
		return &evaluator.EvaluationResult{
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	overallScore := totalScore / float64(len(perInvocation))
	return &evaluator.EvaluationResult{
		OverallScore:         overallScore,
		OverallStatus:        e.statusForScore(overallScore),
		PerInvocationResults: perInvocation,
	}, nil
}

func (e *toolTrajectoryEvaluator) statusForScore(score float64) status.EvalStatus {
	if score >= e.evalMetric.Threshold {
		return status.EvalStatusPassed
	}
	return status.EvalStatusFailed
}

func getToolCalls(invocation *evalset.Invocation) []*evalset.FunctionCall {
	if invocation == nil || invocation.IntermediateData == nil {
		return nil
	}
	return invocation.IntermediateData.ToolUses
}

func toolCallsEqual(actual, expected []*evalset.FunctionCall) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i].Name != expected[i].Name {
			return false
		}
		if !reflect.DeepEqual(actual[i].Args, expected[i].Args) {
			return false
		}
	}
	return true
}
