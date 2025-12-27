//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubricknowledgerecall

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	rmessagesconstructor "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/rubricknowledgerecall"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	rresponsescorer "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/rubricresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
)

type options struct {
	messagesConstructor   messagesconstructor.MessagesConstructor
	responsescorer        responsescorer.ResponseScorer
	samplesAggregator     samplesaggregator.SamplesAggregator
	invocationsAggregator invocationsaggregator.InvocationsAggregator
}

func newOptions(opt ...Option) *options {
	opts := &options{
		messagesConstructor:   rmessagesconstructor.New(),
		responsescorer:        rresponsescorer.New(),
		samplesAggregator:     majorityvote.New(),
		invocationsAggregator: average.New(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option customizes RubricKnowledgeRecall evaluator dependencies.
type Option func(*options)

// WithMessagesConstructor sets the prompt builder for knowledge recall.
func WithMessagesConstructor(mc messagesconstructor.MessagesConstructor) Option {
	return func(o *options) {
		o.messagesConstructor = mc
	}
}

// WithResponsescorer sets the response scorer implementation.
func WithResponsescorer(rs responsescorer.ResponseScorer) Option {
	return func(o *options) {
		o.responsescorer = rs
	}
}

// WithSamplesAggregator sets how multiple judge samples are reduced.
func WithSamplesAggregator(sa samplesaggregator.SamplesAggregator) Option {
	return func(o *options) {
		o.samplesAggregator = sa
	}
}

// WithInvocationsAggregator sets how per-invocation scores are aggregated.
func WithInvocationsAggregator(ia invocationsaggregator.InvocationsAggregator) Option {
	return func(o *options) {
		o.invocationsAggregator = ia
	}
}
