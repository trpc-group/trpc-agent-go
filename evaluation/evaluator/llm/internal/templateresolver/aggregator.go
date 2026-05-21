//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package templateresolver

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
)

const (
	// SampleAggregatorMajorityVoteName identifies the default samples aggregator.
	SampleAggregatorMajorityVoteName = "majority_vote"
	// InvocationAggregatorAverageName identifies the default invocations aggregator.
	InvocationAggregatorAverageName = "average"
)

// ResolveSamplesAggregator returns the samples aggregator identified by name.
func ResolveSamplesAggregator(name string) (samplesaggregator.SamplesAggregator, error) {
	switch name {
	case "", SampleAggregatorMajorityVoteName:
		return majorityvote.New(), nil
	default:
		return nil, fmt.Errorf("unsupported samples aggregator %q", name)
	}
}

// ResolveInvocationsAggregator returns the invocations aggregator identified by name.
func ResolveInvocationsAggregator(name string) (invocationsaggregator.InvocationsAggregator, error) {
	switch name {
	case "", InvocationAggregatorAverageName:
		return average.New(), nil
	default:
		return nil, fmt.Errorf("unsupported invocations aggregator %q", name)
	}
}
