//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package verifierpairwise evaluates pairwise candidate preference using an LLM verifier.
package verifierpairwise

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// EvaluatorName is the registered evaluator name for pairwise LLM verifier scoring.
	EvaluatorName = "llm_verifier_pairwise"
)

type verifierEvaluator struct {
	llmBaseEvaluator      llm.LLMEvaluator
	messagesConstructor   messagesconstructor.MessagesConstructor
	responsescorer        responsescorer.ResponseScorer
	samplesAggregator     samplesaggregator.SamplesAggregator
	invocationsAggregator invocationsaggregator.InvocationsAggregator
}

// New returns a pairwise LLM verifier evaluator.
func New(opt ...Option) evaluator.Evaluator {
	opts := newOptions(opt...)
	e := &verifierEvaluator{
		messagesConstructor:   opts.messagesConstructor,
		responsescorer:        opts.responsescorer,
		samplesAggregator:     opts.samplesAggregator,
		invocationsAggregator: opts.invocationsAggregator,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e
}

// Name returns the evaluator name.
func (e *verifierEvaluator) Name() string {
	return EvaluatorName
}

// Description returns the evaluator description.
func (e *verifierEvaluator) Description() string {
	return "LLM-as-a-Verifier pairwise preference evaluator"
}

// Evaluate evaluates pairwise candidate preference.
func (e *verifierEvaluator) Evaluate(
	ctx context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages constructs verifier judge messages.
func (e *verifierEvaluator) ConstructMessages(
	ctx context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) ([]model.Message, error) {
	return e.messagesConstructor.ConstructMessages(ctx, actuals, expecteds, evalMetric)
}

// StructuredOutput returns the structured output schema for verifier scoring.
func (e *verifierEvaluator) StructuredOutput(
	ctx context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*model.StructuredOutput, error) {
	constructor, ok := e.messagesConstructor.(messagesconstructor.StructuredOutputMessagesConstructor)
	if !ok {
		return nil, nil
	}
	return constructor.StructuredOutput(ctx, actuals, expecteds, evalMetric)
}

// ScoreBasedOnResponse scores the judge response.
func (e *verifierEvaluator) ScoreBasedOnResponse(
	ctx context.Context,
	response *model.Response,
	evalMetric *metric.EvalMetric,
) (*evaluator.ScoreResult, error) {
	return e.responsescorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

// AggregateSamples aggregates multiple verifier samples.
func (e *verifierEvaluator) AggregateSamples(
	ctx context.Context,
	samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric,
) (*evaluator.PerInvocationResult, error) {
	return e.samplesAggregator.AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations aggregates per-invocation verifier results.
func (e *verifierEvaluator) AggregateInvocations(
	ctx context.Context,
	results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	return e.invocationsAggregator.AggregateInvocations(ctx, results, evalMetric)
}
