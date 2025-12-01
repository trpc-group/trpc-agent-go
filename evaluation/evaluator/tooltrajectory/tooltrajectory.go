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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// toolTrajectoryEvaluator is a tool trajectory evaluator implementation for evaluator.
type toolTrajectoryEvaluator struct {
}

// New creates a new trajectory evaluator.
func New() evaluator.Evaluator {
	return &toolTrajectoryEvaluator{}
}

// Name returns the name of this evaluator.
func (e *toolTrajectoryEvaluator) Name() string {
	return "tool_trajectory_avg_score"
}

// Description returns a description of what this evaluator does.
func (e *toolTrajectoryEvaluator) Description() string {
	return "Evaluates the accuracy of tool usage trajectory including sequence and arguments"
}

// Evaluate compares tool usage trajectories between actual and expected invocations.
func (e *toolTrajectoryEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.ToolTrajectory == nil {
		return nil, errors.New("tool trajectory criterion not configured")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("tooltrajectory: actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	perInvocation := make([]evaluator.PerInvocationResult, 0, len(actuals))
	var totalScore float64
	for i := range len(actuals) {
		actual := actuals[i]
		expected := expecteds[i]
		score := 0.0
		reason := ""
		ok, err := toolCallsMatch(actual, expected, evalMetric.Criterion.ToolTrajectory)
		if err != nil {
			reason = err.Error()
		} else if ok {
			score = 1.0
		}
		status := e.statusForScore(score, evalMetric)
		perInvocation = append(perInvocation, evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expected,
			Score:              score,
			Status:             status,
			Details: &evaluator.PerInvocationDetails{
				Reason: reason,
				Score:  score,
			},
		})
		totalScore += score
	}
	if len(perInvocation) == 0 {
		return &evaluator.EvaluateResult{
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	overallScore := totalScore / float64(len(perInvocation))
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        e.statusForScore(overallScore, evalMetric),
		PerInvocationResults: perInvocation,
	}, nil
}

func (e *toolTrajectoryEvaluator) statusForScore(score float64, evalMetric *metric.EvalMetric) status.EvalStatus {
	if score >= evalMetric.Threshold {
		return status.EvalStatusPassed
	}
	return status.EvalStatusFailed
}

func toolCallsMatch(actual, expected *evalset.Invocation,
	criterion *ctooltrajectory.ToolTrajectoryCriterion) (bool, error) {
	ok, err := criterion.Match(actual, expected)
	if err != nil {
		return false, fmt.Errorf("tool trajectory mismatch: %w", err)
	}
	return ok, nil
}
