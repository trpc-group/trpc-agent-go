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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
)

// LLMEvaluator defines the LLM-backed evaluator contract.
type LLMEvaluator interface {
	evaluator.Evaluator
	// ConstructMessages builds prompts for the judge model.
	ConstructMessages(actual, expected *evalset.Invocation, evalMetric *metric.EvalMetric) ([]model.Message, error)
	// ScoreBasedOnResponse extracts a score from the judge response.
	ScoreBasedOnResponse(resp *model.Response, evalMetric *metric.EvalMetric) (*evalresult.ScoreResult, error)
	// AggregateSamples summarizes multiple sample scores for one invocation.
	AggregateSamples(samples []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error)
	// AggregateInvocations aggregates per-invocation results into the final evaluation.
	AggregateInvocations(results []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error)
}

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
	if evalMetric == nil ||
		evalMetric.Criterion == nil ||
		evalMetric.Criterion.LLMJudge == nil ||
		evalMetric.Criterion.LLMJudge.JudgeModel == nil ||
		evalMetric.Criterion.LLMJudge.JudgeModel.Generation == nil {
		return nil, fmt.Errorf("missing required fields in eval metric")
	}
	if evalMetric.Criterion.LLMJudge.JudgeModel.NumSamples <= 0 {
		return nil, fmt.Errorf("num samples must be greater than 0")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	for i := range actuals {
		actual := actuals[i]
		expected := expecteds[i]
		messages, err := r.ConstructMessages(actual, expected, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("construct messages: %w", err)
		}
		numSamples := evalMetric.Criterion.LLMJudge.JudgeModel.NumSamples
		samples := make([]*evaluator.PerInvocationResult, 0, numSamples)
		for range numSamples {
			response, err := judgeModelResponse(ctx, messages, evalMetric)
			if err != nil {
				return nil, fmt.Errorf("judge model response: %w", err)
			}
			score, err := r.ScoreBasedOnResponse(response, evalMetric)
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
			})
		}
		perInvocationResult, err := r.AggregateSamples(samples, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("aggregate samples: %w", err)
		}
		results = append(results, perInvocationResult)
	}
	return r.AggregateInvocations(results, evalMetric)
}

// AggregateInvocations delegates invocation aggregation to the concrete evaluator.
func (r *LLMBaseEvaluator) AggregateInvocations(results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return r.LLMEvaluator.AggregateInvocations(results, evalMetric)
}

// AggregateSamples delegates sample aggregation to the concrete evaluator.
func (r *LLMBaseEvaluator) AggregateSamples(samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return r.LLMEvaluator.AggregateSamples(samples, evalMetric)
}

// ScoreBasedOnResponse delegates response scoring to the concrete evaluator.
func (r *LLMBaseEvaluator) ScoreBasedOnResponse(resp *model.Response,
	evalMetric *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	return r.LLMEvaluator.ScoreBasedOnResponse(resp, evalMetric)
}

// ConstructMessages delegates prompt construction to the concrete evaluator.
func (r *LLMBaseEvaluator) ConstructMessages(actual, expected *evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	return r.LLMEvaluator.ConstructMessages(actual, expected, evalMetric)
}

// judgeModelResponse calls the judge model and returns the final response.
func judgeModelResponse(ctx context.Context, messages []model.Message,
	evalMetric *metric.EvalMetric) (*model.Response, error) {
	judgeModel := evalMetric.Criterion.LLMJudge.JudgeModel
	req := model.Request{
		Messages:         messages,
		GenerationConfig: *judgeModel.Generation,
	}
	req.GenerationConfig.Stream = false
	modelInstance, err := provider.Model(
		judgeModel.ProviderName,
		judgeModel.ModelName,
		provider.WithAPIKey(judgeModel.APIKey),
		provider.WithBaseURL(judgeModel.BaseURL),
		provider.WithExtraFields(judgeModel.ExtraFields),
	)
	if err != nil {
		return nil, fmt.Errorf("create model instance: %w", err)
	}
	responses, err := modelInstance.GenerateContent(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("generate response: %w", err)
	}
	for response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("response error: %v", response.Error)
		}
		if response.IsFinalResponse() {
			return response, nil
		}
	}
	return nil, fmt.Errorf("no final response")
}
