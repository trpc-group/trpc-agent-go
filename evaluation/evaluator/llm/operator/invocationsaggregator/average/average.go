//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package average aggregates invocation results using arithmetic mean.
package average

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type averageInvocationsAggregator struct {
}

// New returns an invocations aggregator that averages evaluated scores.
func New() invocationsaggregator.InvocationsAggregator {
	return &averageInvocationsAggregator{}
}

// AggregateInvocations summarizes per-invocation results into an overall score while skipping not-evaluated entries.
func (a *averageInvocationsAggregator) AggregateInvocations(ctx context.Context,
	results []*evaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	sumScore := 0.0
	numEvaluated := 0.0
	for _, result := range results {
		if result.Status == status.EvalStatusNotEvaluated {
			continue
		}
		numEvaluated++
		sumScore += result.Score
	}
	if numEvaluated == 0 {
		return &evaluator.EvaluateResult{
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	overallScore := sumScore / numEvaluated
	overallStatus := status.EvalStatusPassed
	if overallScore < evalMetric.Threshold {
		overallStatus = status.EvalStatusFailed
	}
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        overallStatus,
		PerInvocationResults: results,
	}, nil
}
