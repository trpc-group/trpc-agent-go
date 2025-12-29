//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubricknowledgerecall

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	knmessages "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/rubricknowledgerecall"
	rresponsescorer "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/rubricresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type optionStubMessagesConstructor struct{}

func (s *optionStubMessagesConstructor) ConstructMessages(context.Context, []*evalset.Invocation, []*evalset.Invocation,
	*metric.EvalMetric) ([]model.Message, error) {
	return nil, nil
}

type optionStubResponseScorer struct{}

func (s *optionStubResponseScorer) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return nil, nil
}

type optionStubSamplesAggregator struct{}

func (s *optionStubSamplesAggregator) AggregateSamples(context.Context, []*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return nil, nil
}

type optionStubInvocationsAggregator struct{}

func (s *optionStubInvocationsAggregator) AggregateInvocations(context.Context, []*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return nil, nil
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()

	require.NotNil(t, opts.messagesConstructor)
	require.NotNil(t, opts.responsescorer)
	require.NotNil(t, opts.samplesAggregator)
	require.NotNil(t, opts.invocationsAggregator)

	assert.IsType(t, knmessages.New(), opts.messagesConstructor)
	assert.IsType(t, rresponsescorer.New(), opts.responsescorer)
	assert.IsType(t, majorityvote.New(), opts.samplesAggregator)
	assert.IsType(t, average.New(), opts.invocationsAggregator)
}

func TestNewOptionsOverrides(t *testing.T) {
	mc := &optionStubMessagesConstructor{}
	rs := &optionStubResponseScorer{}
	sa := &optionStubSamplesAggregator{}
	ia := &optionStubInvocationsAggregator{}

	opts := newOptions(
		WithMessagesConstructor(mc),
		WithResponsescorer(rs),
		WithSamplesAggregator(sa),
		WithInvocationsAggregator(ia),
	)

	assert.Same(t, mc, opts.messagesConstructor)
	assert.Same(t, rs, opts.responsescorer)
	assert.Same(t, sa, opts.samplesAggregator)
	assert.Same(t, ia, opts.invocationsAggregator)
}
