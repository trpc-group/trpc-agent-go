//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	fmessagesconstructor "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	fresponsescorer "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/finalresponse"
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
		messagesConstructor:   fmessagesconstructor.New(),
		responsescorer:        fresponsescorer.New(),
		samplesAggregator:     majorityvote.New(),
		invocationsAggregator: average.New(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option customizes FinalResponse evaluator dependencies.
type Option func(*options)

// WithMessagesConstructor sets the prompt builder for final responses.
func WithMessagesConstructor(messagesConstructor messagesconstructor.MessagesConstructor) Option {
	return func(o *options) {
		o.messagesConstructor = messagesConstructor
	}
}

// WithResponsescorer sets the response scorer implementation.
func WithResponsescorer(responsescorer responsescorer.ResponseScorer) Option {
	return func(o *options) {
		o.responsescorer = responsescorer
	}
}

// WithSamplesAggregator sets how multiple judge samples are reduced.
func WithSamplesAggregator(samplesAggregator samplesaggregator.SamplesAggregator) Option {
	return func(o *options) {
		o.samplesAggregator = samplesAggregator
	}
}

// WithInvocationsAggregator sets how per-invocation scores are aggregated.
func WithInvocationsAggregator(invocationsAggregator invocationsaggregator.InvocationsAggregator) Option {
	return func(o *options) {
		o.invocationsAggregator = invocationsAggregator
	}
}
