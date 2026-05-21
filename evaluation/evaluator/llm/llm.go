//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llm provides base helpers for LLM-backed evaluators.
package llm

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/judger"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/templateresolver"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// LLMEvaluator defines the LLM-backed evaluator contract.
type LLMEvaluator interface {
	evaluator.Evaluator
	messagesconstructor.MessagesConstructor
	responsescorer.ResponseScorer
	samplesaggregator.SamplesAggregator
	invocationsaggregator.InvocationsAggregator
}

const templateEvaluatorName = "llm_judge_template"

// LLMBaseEvaluator hosts shared orchestration logic for LLM evaluators.
type LLMBaseEvaluator struct {
	LLMEvaluator LLMEvaluator // LLMEvaluator is the concrete LLM evaluator implementation.
}

// New constructs an LLMBaseEvaluator wrapper around the concrete evaluator.
func New(llmEvaluator LLMEvaluator) LLMEvaluator {
	return &LLMBaseEvaluator{LLMEvaluator: llmEvaluator}
}

// Name returns the evaluator name.
func (r *LLMBaseEvaluator) Name() string {
	return "llm_base_evaluator"
}

// Description describes the evaluator.
func (r *LLMBaseEvaluator) Description() string {
	return "Base evaluator for LLM judge"
}

// Evaluate runs the judge model over paired invocations and aggregates results.
func (r *LLMBaseEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("missing required fields in eval metric")
	}
	judgeCriterion := evalMetric.Criterion.LLMJudge
	var judgeRunner runner.Runner
	if judgeCriterion.JudgeRunnerOptions != nil {
		judgeRunner = judgeCriterion.JudgeRunnerOptions.Runner
	}
	if judgeRunner == nil && judgeCriterion.JudgeModel == nil {
		return nil, fmt.Errorf("missing required fields in eval metric")
	}
	numSamples := 1
	if judgeRunner == nil {
		numSamplesPtr := judgeCriterion.JudgeModel.NumSamples
		if numSamplesPtr == nil {
			defaultNumSamples := llm.DefaultNumSamples
			numSamplesPtr = &defaultNumSamples
		}
		numSamples = *numSamplesPtr
	}
	if numSamples <= 0 {
		return nil, fmt.Errorf("num samples must be greater than 0")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	structuredOutput, err := r.resolveStructuredOutput(ctx, evalMetric)
	if err != nil {
		return nil, fmt.Errorf("resolve structured output: %w", err)
	}
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	for i := range actuals {
		actual := actuals[i]
		expected := expecteds[i]
		messages, err := r.ConstructMessages(ctx, actuals[:i+1], expecteds[:i+1], evalMetric)
		if err != nil {
			return nil, fmt.Errorf("construct messages: %w", err)
		}
		samples := make([]*evaluator.PerInvocationResult, 0, numSamples)
		for range numSamples {
			response, err := judger.Judge(ctx, messages, evalMetric, judger.WithStructuredOutput(structuredOutput))
			if err != nil {
				return nil, fmt.Errorf("judge response: %w", err)
			}
			score, err := r.ScoreBasedOnResponse(ctx, response, evalMetric)
			if err != nil {
				return nil, fmt.Errorf("score based on response: %w", err)
			}
			evalStatus := status.EvalStatusPassed
			if score.Score < evalMetric.Threshold {
				evalStatus = status.EvalStatusFailed
			}
			samples = append(samples, &evaluator.PerInvocationResult{
				ActualInvocation:   actual,
				ExpectedInvocation: expected,
				Score:              score.Score,
				Status:             evalStatus,
				Details: &evaluator.PerInvocationDetails{
					Reason:       score.Reason,
					Score:        score.Score,
					RubricScores: score.RubricScores,
				},
			})
		}
		perInvocationResult, err := r.AggregateSamples(ctx, samples, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("aggregate samples: %w", err)
		}
		results = append(results, perInvocationResult)
	}
	return r.AggregateInvocations(ctx, results, evalMetric)
}

// AggregateInvocations delegates invocation aggregation to the concrete evaluator.
func (r *LLMBaseEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return r.LLMEvaluator.AggregateInvocations(ctx, results, evalMetric)
}

// AggregateSamples delegates sample aggregation to the concrete evaluator.
func (r *LLMBaseEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return r.LLMEvaluator.AggregateSamples(ctx, samples, evalMetric)
}

// ScoreBasedOnResponse delegates response scoring to the concrete evaluator.
func (r *LLMBaseEvaluator) ScoreBasedOnResponse(ctx context.Context, resp *model.Response,
	evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return r.LLMEvaluator.ScoreBasedOnResponse(ctx, resp, evalMetric)
}

// ConstructMessages delegates prompt construction to the concrete evaluator.
func (r *LLMBaseEvaluator) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return r.LLMEvaluator.ConstructMessages(ctx, actuals, expecteds, evalMetric)
}

func (r *LLMBaseEvaluator) resolveStructuredOutput(ctx context.Context,
	evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	if ctx == nil || evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, nil
	}
	if resolveEvaluatorName(evalMetric) != templateEvaluatorName {
		return nil, nil
	}
	templateOptions := evalMetric.Criterion.LLMJudge.Template
	if templateOptions == nil {
		return nil, nil
	}
	return templateresolver.StructuredOutput(templateOptions.ResponseScorerName)
}

func resolveEvaluatorName(evalMetric *metric.EvalMetric) string {
	if evalMetric == nil {
		return ""
	}
	if evalMetric.EvaluatorName != "" {
		return evalMetric.EvaluatorName
	}
	return evalMetric.MetricName
}
