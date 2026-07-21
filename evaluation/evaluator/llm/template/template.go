//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package template evaluates prompt-defined LLM judge metrics.
package template

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	tmessagesconstructor "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/template"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvaluatorName is the registered evaluator name for template-based LLM judging.
const EvaluatorName = "llm_judge_template"

type templateEvaluator struct {
	llmBaseEvaluator    llm.LLMEvaluator
	messagesConstructor messagesconstructor.MessagesConstructor
	operatorRegistry    operatorregistry.Registry
}

// Option configures the template evaluator.
type Option func(*templateEvaluator)

// WithOperatorRegistry sets the LLM operator registry.
func WithOperatorRegistry(registry operatorregistry.Registry) Option {
	return func(e *templateEvaluator) {
		e.operatorRegistry = registry
	}
}

// New returns the template evaluator.
func New(opt ...Option) evaluator.Evaluator {
	e := &templateEvaluator{}
	for _, o := range opt {
		o(e)
	}
	if e.operatorRegistry == nil {
		e.operatorRegistry = operatorregistry.New()
	}
	e.messagesConstructor = tmessagesconstructor.New(
		tmessagesconstructor.WithOperatorRegistry(e.operatorRegistry),
	)
	e.llmBaseEvaluator = llm.New(e)
	return e
}

// Name returns the evaluator name.
func (e *templateEvaluator) Name() string {
	return EvaluatorName
}

// Description returns the evaluator description.
func (e *templateEvaluator) Description() string {
	return "LLM template judge evaluator"
}

// Evaluate runs template-based LLM evaluation.
func (e *templateEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages builds judge prompts from template configuration.
func (e *templateEvaluator) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return e.messagesConstructor.ConstructMessages(ctx, actuals, expecteds, evalMetric)
}

// StructuredOutput delegates structured output schema construction to the prompt builder.
func (e *templateEvaluator) StructuredOutput(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	constructor, ok := e.messagesConstructor.(messagesconstructor.StructuredOutputMessagesConstructor)
	if !ok {
		return nil, nil
	}
	return constructor.StructuredOutput(ctx, actuals, expecteds, evalMetric)
}

// ScoreBasedOnResponse scores the judge response with the configured scorer.
func (e *templateEvaluator) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	operators, err := e.operatorRegistry.Resolve(evalMetric)
	if err != nil {
		return nil, err
	}
	return operators.ResponseScorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

// AggregateSamples aggregates samples with the configured strategy.
func (e *templateEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	operators, err := e.operatorRegistry.Resolve(evalMetric)
	if err != nil {
		return nil, err
	}
	return operators.SamplesAggregator.AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations aggregates invocation results with the configured strategy.
func (e *templateEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	operators, err := e.operatorRegistry.Resolve(evalMetric)
	if err != nil {
		return nil, err
	}
	return operators.InvocationsAggregator.AggregateInvocations(ctx, results, evalMetric)
}
