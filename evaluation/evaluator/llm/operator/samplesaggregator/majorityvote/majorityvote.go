//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package majorityvote picks a representative sample based on vote counts.
package majorityvote

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type majorityVoteSamplesAggregator struct {
}

// New returns a samples aggregator that selects a representative by majority vote.
func New() samplesaggregator.SamplesAggregator {
	return &majorityVoteSamplesAggregator{}
}

// AggregateSamples resolves multiple judge samples to one invocation result, preferring the majority status.
func (s *majorityVoteSamplesAggregator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}
	positiveResults := make([]*evaluator.PerInvocationResult, 0)
	negativeResults := make([]*evaluator.PerInvocationResult, 0)
	for _, sample := range samples {
		if sample.Status == status.EvalStatusNotEvaluated {
			continue
		}
		if sample.Score >= evalMetric.Threshold {
			positiveResults = append(positiveResults, sample)
		} else {
			negativeResults = append(negativeResults, sample)
		}
	}
	if len(positiveResults) == 0 && len(negativeResults) == 0 {
		return samples[0], nil
	}
	if len(positiveResults) > len(negativeResults) {
		return positiveResults[0], nil
	} else {
		// On tie or majority fail, return a representative failing sample to preserve conservative scoring.
		return negativeResults[0], nil
	}
}
