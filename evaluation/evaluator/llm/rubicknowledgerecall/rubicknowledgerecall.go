//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubicknowledgerecall evaluates knowledge recall using LLM judges.
package rubicknowledgerecall

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
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

type rubicKnowledgeRecallEvaluator struct {
	llmBaseEvaluator      llm.LLMEvaluator
	messagesConstructor   messagesconstructor.MessagesConstructor
	responsescorer        responsescorer.ResponseScorer
	samplesAggregator     samplesaggregator.SamplesAggregator
	invocationsAggregator invocationsaggregator.InvocationsAggregator
}

// New builds the rubic knowledge recall evaluator.
func New(opt ...Option) evaluator.Evaluator {
	opts := newOptions(opt...)
	e := &rubicKnowledgeRecallEvaluator{
		messagesConstructor:   opts.messagesConstructor,
		responsescorer:        opts.responsescorer,
		samplesAggregator:     opts.samplesAggregator,
		invocationsAggregator: opts.invocationsAggregator,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e
}

func (e *rubicKnowledgeRecallEvaluator) Name() string {
	return "llm_rubic_knowledge_recall"
}

func (e *rubicKnowledgeRecallEvaluator) Description() string {
	return "LLM rubic knowledge recall evaluator"
}

func (e *rubicKnowledgeRecallEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

func (e *rubicKnowledgeRecallEvaluator) ConstructMessages(ctx context.Context, actual, expected *evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return e.messagesConstructor.ConstructMessages(ctx, actual, expected, evalMetric)
}

func (e *rubicKnowledgeRecallEvaluator) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	return e.responsescorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

func (e *rubicKnowledgeRecallEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return e.samplesAggregator.AggregateSamples(ctx, samples, evalMetric)
}

func (e *rubicKnowledgeRecallEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.invocationsAggregator.AggregateInvocations(ctx, results, evalMetric)
}
