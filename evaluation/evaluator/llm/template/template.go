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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/templateresolver"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	tmessagesconstructor "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/template"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvaluatorName is the registered evaluator name for template-based LLM judging.
const EvaluatorName = "llm_judge_template"

type templateEvaluator struct {
	llmBaseEvaluator    llm.LLMEvaluator
	messagesConstructor messagesconstructor.MessagesConstructor
}

// New returns the template evaluator.
func New() evaluator.Evaluator {
	e := &templateEvaluator{
		messagesConstructor: tmessagesconstructor.New(),
	}
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

// ScoreBasedOnResponse scores the judge response with the configured scorer.
func (e *templateEvaluator) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	scorerName, err := responseScorerName(evalMetric)
	if err != nil {
		return nil, err
	}
	scorer, err := templateresolver.ResolveResponseScorer(scorerName)
	if err != nil {
		return nil, err
	}
	return scorer.ScoreBasedOnResponse(ctx, response, evalMetric)
}

// AggregateSamples aggregates samples with the configured strategy.
func (e *templateEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	aggregator, err := templateresolver.ResolveSamplesAggregator(sampleAggregatorName(templateOptions))
	if err != nil {
		return nil, err
	}
	return aggregator.AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations aggregates invocation results with the configured strategy.
func (e *templateEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	aggregator, err := templateresolver.ResolveInvocationsAggregator(invocationAggregatorName(templateOptions))
	if err != nil {
		return nil, err
	}
	return aggregator.AggregateInvocations(ctx, results, evalMetric)
}

func responseScorerName(evalMetric *metric.EvalMetric) (string, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return "", err
	}
	if templateOptions.ResponseScorerName == "" {
		return "", fmt.Errorf("template responseScorerName is empty")
	}
	return templateOptions.ResponseScorerName, nil
}

func sampleAggregatorName(templateOptions *metricllm.JudgeTemplateOptions) string {
	if templateOptions == nil || templateOptions.SampleAggregatorName == "" {
		return templateresolver.SampleAggregatorMajorityVoteName
	}
	return templateOptions.SampleAggregatorName
}

func invocationAggregatorName(templateOptions *metricllm.JudgeTemplateOptions) string {
	if templateOptions == nil || templateOptions.InvocationAggregatorName == "" {
		return templateresolver.InvocationAggregatorAverageName
	}
	return templateOptions.InvocationAggregatorName
}

func judgeTemplateOptions(evalMetric *metric.EvalMetric) (*metricllm.JudgeTemplateOptions, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("missing llm judge criterion")
	}
	if evalMetric.Criterion.LLMJudge.Template == nil {
		return nil, fmt.Errorf("template is nil")
	}
	return evalMetric.Criterion.LLMJudge.Template, nil
}
