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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
)

const (
	// metricName matches ADK Python's PrebuiltMetrics.TOOL_TRAJECTORY_AVG_SCORE.
	metricName = "tool_trajectory_avg_score"
	// defaultThreshold keeps parity with ADK Python where the metric defaults to 1.0.
	defaultThreshold = 1.0
)

// Option configures the tool trajectory evaluator.
type Option func(*toolTrajectoryEvaluator)

// WithThreshold overrides the pass threshold.
func WithThreshold(threshold float64) Option {
	return func(e *toolTrajectoryEvaluator) {
		e.threshold = threshold
	}
}

// toolTrajectoryEvaluator implements trajectory-based evaluation.
type toolTrajectoryEvaluator struct {
	threshold float64
}

var _ evaluator.Evaluator = (*toolTrajectoryEvaluator)(nil)

// New creates a new trajectory evaluator.
func New(opt ...Option) evaluator.Evaluator {
	e := &toolTrajectoryEvaluator{threshold: defaultThreshold}
	for _, o := range opt {
		o(e)
	}
	return e
}

// Evaluate compares tool usage trajectories between actual and expected invocations.
func (e *toolTrajectoryEvaluator) Evaluate(ctx context.Context, actual, expected []*evalset.Invocation) (*evaluator.EvaluationResult, error) {
	if len(actual) != len(expected) {
		return nil, fmt.Errorf("tooltrajectory: actual invocations (%d) and expected invocations (%d) count mismatch", len(actual), len(expected))
	}

	perInvocation := make([]evaluator.PerInvocationResult, 0, len(actual))
	var totalScore float64

	for idx := range actual {
		actualInv := actual[idx]
		expectedInv := expected[idx]

		actualCalls := getToolCalls(actualInv)
		expectedCalls := getToolCalls(expectedInv)

		score := 0.0
		if toolCallsEqual(actualCalls, expectedCalls) {
			score = 1.0
		}

		status := e.statusForScore(score)
		perInvocation = append(perInvocation, evaluator.PerInvocationResult{
			ActualInvocation:   actualInv,
			ExpectedInvocation: expectedInv,
			Score:              score,
			Status:             status,
		})
		totalScore += score
	}

	if len(perInvocation) == 0 {
		return &evaluator.EvaluationResult{
			OverallStatus: evalresult.EvalStatusNotEvaluated,
		}, nil
	}

	overallScore := totalScore / float64(len(perInvocation))
	return &evaluator.EvaluationResult{
		OverallScore:         overallScore,
		OverallStatus:        e.statusForScore(overallScore),
		PerInvocationResults: perInvocation,
	}, nil
}

func (e *toolTrajectoryEvaluator) statusForScore(score float64) evalresult.EvalStatus {
	if score >= e.threshold {
		return evalresult.EvalStatusPassed
	}
	return evalresult.EvalStatusFailed
}

func getToolCalls(invocation *evalset.Invocation) []evalset.FunctionCall {
	if invocation == nil || invocation.IntermediateData == nil {
		return nil
	}
	return invocation.IntermediateData.ToolUses
}

func toolCallsEqual(actual, expected []evalset.FunctionCall) bool {
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

// Name returns the name of this evaluator.
func (e *toolTrajectoryEvaluator) Name() string {
	return metricName
}

// Description returns a description of what this evaluator does.
func (e *toolTrajectoryEvaluator) Description() string {
	return "Evaluates the accuracy of tool usage trajectory including sequence and arguments"
}
