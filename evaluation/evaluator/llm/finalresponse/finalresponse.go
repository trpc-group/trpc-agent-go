//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse implements an LLM judge for final responses.
package finalresponse

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

// finalResponseEvaluator evaluates final responses via an LLM judge.
type finalResponseEvaluator struct {
	llmBaseEvaluator      llm.LLMEvaluator
	messagesConstructor   messagesconstructor.MessagesConstructor
	responsescorer        responsescorer.ResponseScorer
	samplesAggregator     samplesaggregator.SamplesAggregator
	invocationsAggregator invocationsaggregator.InvocationsAggregator
}

// New builds the final response evaluator.
func New(opt ...Option) evaluator.Evaluator {
	opts := newOptions(opt...)
	e := &finalResponseEvaluator{
		messagesConstructor:   opts.messagesConstructor,
		responsescorer:        opts.responsescorer,
		samplesAggregator:     opts.samplesAggregator,
		invocationsAggregator: opts.invocationsAggregator,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e
}

// Name returns the evaluator identifier.
func (e *finalResponseEvaluator) Name() string {
	return "llm_final_response"
}

// Description describes the evaluator purpose.
func (e *finalResponseEvaluator) Description() string {
	return "LLM judge for final responses"
}

// Evaluate runs LLM-based evaluation on final responses.
func (e *finalResponseEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages builds judge prompts from actual and expected responses.
func (e *finalResponseEvaluator) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return e.messagesConstructor.ConstructMessages(ctx, actuals, expecteds, evalMetric)
}

// ScoreBasedOnResponse converts judge feedback to a numeric score.
func (e *finalResponseEvaluator) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return e.responsescorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

// AggregateSamples resolves multiple judge samples to one invocation result.
func (e *finalResponseEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return e.samplesAggregator.AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations summarizes per-invocation results into an overall score.
func (e *finalResponseEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.invocationsAggregator.AggregateInvocations(ctx, results, evalMetric)
}
