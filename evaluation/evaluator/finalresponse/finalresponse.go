//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse provides deterministic evaluation for agent final responses.
package finalresponse

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// finalResponseEvaluator evaluates final responses using deterministic matching criteria.
type finalResponseEvaluator struct {
}

// New creates a new final response evaluator.
func New() evaluator.Evaluator {
	return &finalResponseEvaluator{}
}

// Name returns the evaluator identifier.
func (e *finalResponseEvaluator) Name() string {
	return "final_response_avg_score"
}

// Description describes the evaluator purpose.
func (e *finalResponseEvaluator) Description() string {
	return "Evaluates agent final responses against expected outputs"
}

// Evaluate compares final responses between actual and expected invocations.
func (e *finalResponseEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.FinalResponse == nil {
		return nil, errors.New("final response criterion not configured")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("finalresponse: actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	perInvocation := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	var totalScore float64
	for i := range len(actuals) {
		actual := actuals[i]
		expected := expecteds[i]
		score := 0.0
		reason := ""
		ok, err := finalResponsesMatch(ctx, actual, expected, evalMetric.Criterion.FinalResponse)
		if err != nil {
			reason = err.Error()
		} else if ok {
			score = 1.0
		} else {
			reason = "final response mismatch"
		}
		invocationStatus := e.statusForScore(score, evalMetric)
		perInvocation = append(perInvocation, &evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expected,
			Score:              score,
			Status:             invocationStatus,
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

// statusForScore maps a numeric score to an evaluation status based on the metric threshold.
func (e *finalResponseEvaluator) statusForScore(score float64, evalMetric *metric.EvalMetric) status.EvalStatus {
	if score >= evalMetric.Threshold {
		return status.EvalStatusPassed
	}
	return status.EvalStatusFailed
}

// finalResponsesMatch performs deterministic matching for the configured final response criterion.
func finalResponsesMatch(ctx context.Context, actual, expected *evalset.Invocation,
	criterion *cfinalresponse.FinalResponseCriterion) (bool, error) {
	ok, err := criterion.Match(ctx, actual, expected)
	if err != nil {
		return false, fmt.Errorf("final response mismatch: %w", err)
	}
	return ok, nil
}
