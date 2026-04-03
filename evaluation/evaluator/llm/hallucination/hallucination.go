//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hallucination evaluates final answers for hallucinations using LLM judges.
package hallucination

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

type hallucinationEvaluator struct {
	llmBaseEvaluator      llm.LLMEvaluator
	messagesConstructor   messagesconstructor.MessagesConstructor
	responsescorer        responsescorer.ResponseScorer
	samplesAggregator     samplesaggregator.SamplesAggregator
	invocationsAggregator invocationsaggregator.InvocationsAggregator
}

// New builds the hallucination evaluator.
func New(opt ...Option) evaluator.Evaluator {
	opts := newOptions(opt...)
	e := &hallucinationEvaluator{
		messagesConstructor:   opts.messagesConstructor,
		responsescorer:        opts.responsescorer,
		samplesAggregator:     opts.samplesAggregator,
		invocationsAggregator: opts.invocationsAggregator,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e
}

// Name returns the name of the evaluator.
func (e *hallucinationEvaluator) Name() string {
	return "llm_hallucinations"
}

// Description returns the description of the evaluator.
func (e *hallucinationEvaluator) Description() string {
	return "LLM hallucination evaluator"
}

// Evaluate evaluates the hallucination risk of the agent final answer.
func (e *hallucinationEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages constructs the messages for the evaluator.
func (e *hallucinationEvaluator) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return e.messagesConstructor.ConstructMessages(ctx, actuals, expecteds, evalMetric)
}

// ScoreBasedOnResponse scores the response of the evaluator.
func (e *hallucinationEvaluator) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return e.responsescorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

// AggregateSamples aggregates the samples of the evaluator.
func (e *hallucinationEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return e.samplesAggregator.AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations aggregates the invocations of the evaluator.
func (e *hallucinationEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.invocationsAggregator.AggregateInvocations(ctx, results, evalMetric)
}
