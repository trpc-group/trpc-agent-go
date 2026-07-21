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
	"runtime"

	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/judger"
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

// LLMBaseEvaluator hosts shared orchestration logic for LLM evaluators.
type LLMBaseEvaluator struct {
	LLMEvaluator LLMEvaluator // LLMEvaluator is the concrete LLM evaluator implementation.
}

type sampleCollectionRequest struct {
	actual                   *evalset.Invocation
	expected                 *evalset.Invocation
	messages                 []model.Message
	evalMetric               *metric.EvalMetric
	structuredOutput         *model.StructuredOutput
	numSamples               int
	sampleParallelismEnabled bool
	sampleParallelism        int
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
	if judgeRunner != nil {
		if judgeCriterion.JudgeRunnerOptions.NumSamples != nil {
			numSamples = *judgeCriterion.JudgeRunnerOptions.NumSamples
		}
	} else {
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
	if judgeCriterion.SampleParallelism < 0 {
		return nil, fmt.Errorf("sample parallelism must be non-negative")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	for i := range actuals {
		actual := actuals[i]
		expected := expecteds[i]
		currentActuals := actuals[:i+1]
		currentExpecteds := expecteds[:i+1]
		messages, err := r.ConstructMessages(ctx, currentActuals, currentExpecteds, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("construct messages: %w", err)
		}
		structuredOutput, err := r.resolveStructuredOutput(ctx, currentActuals, currentExpecteds, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("resolve structured output: %w", err)
		}
		samples, err := r.collectSamples(ctx, &sampleCollectionRequest{
			actual:                   actual,
			expected:                 expected,
			messages:                 messages,
			evalMetric:               evalMetric,
			structuredOutput:         structuredOutput,
			numSamples:               numSamples,
			sampleParallelismEnabled: judgeCriterion.SampleParallelismEnabled,
			sampleParallelism:        judgeCriterion.SampleParallelism,
		})
		if err != nil {
			return nil, err
		}
		perInvocationResult, err := r.AggregateSamples(ctx, samples, evalMetric)
		if err != nil {
			return nil, fmt.Errorf("aggregate samples: %w", err)
		}
		results = append(results, perInvocationResult)
	}
	return r.AggregateInvocations(ctx, results, evalMetric)
}

func (r *LLMBaseEvaluator) collectSamples(ctx context.Context,
	req *sampleCollectionRequest) ([]*evaluator.PerInvocationResult, error) {
	parallelism := resolveSampleParallelism(req.numSamples, req.sampleParallelismEnabled, req.sampleParallelism)
	if parallelism <= 1 {
		return r.collectSamplesSerially(ctx, req)
	}
	return r.collectSamplesInParallel(ctx, req, parallelism)
}

func (r *LLMBaseEvaluator) collectSamplesSerially(ctx context.Context,
	req *sampleCollectionRequest) ([]*evaluator.PerInvocationResult, error) {
	samples := make([]*evaluator.PerInvocationResult, 0, req.numSamples)
	for range req.numSamples {
		sample, err := r.collectOneSample(ctx, req)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

func (r *LLMBaseEvaluator) collectSamplesInParallel(ctx context.Context,
	req *sampleCollectionRequest, parallelism int) ([]*evaluator.PerInvocationResult, error) {
	samples := make([]*evaluator.PerInvocationResult, req.numSamples)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(parallelism)
	for i := range req.numSamples {
		sampleIndex := i
		group.Go(func() error {
			sample, err := r.collectOneSample(groupCtx, req)
			if err != nil {
				return err
			}
			samples[sampleIndex] = sample
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return samples, nil
}

func resolveSampleParallelism(numSamples int, enabled bool, parallelism int) int {
	if !enabled || numSamples <= 1 {
		return 1
	}
	if parallelism <= 0 {
		parallelism = runtime.GOMAXPROCS(0)
	}
	if parallelism > numSamples {
		return numSamples
	}
	return parallelism
}

func (r *LLMBaseEvaluator) collectOneSample(ctx context.Context,
	req *sampleCollectionRequest) (*evaluator.PerInvocationResult, error) {
	response, err := judger.Judge(ctx, req.messages, req.evalMetric, judger.WithStructuredOutput(req.structuredOutput))
	if err != nil {
		return nil, fmt.Errorf("judge response: %w", err)
	}
	score, err := r.ScoreBasedOnResponse(ctx, response, req.evalMetric)
	if err != nil {
		return nil, fmt.Errorf("score based on response: %w", err)
	}
	evalStatus := resolveScoreStatus(score, req.evalMetric.Threshold)
	return &evaluator.PerInvocationResult{
		ActualInvocation:   req.actual,
		ExpectedInvocation: req.expected,
		Score:              score.Score,
		Status:             evalStatus,
		Details: &evaluator.PerInvocationDetails{
			Reason:       score.Reason,
			Score:        score.Score,
			Value:        score.Value,
			RubricScores: score.RubricScores,
		},
	}, nil
}

func resolveScoreStatus(score *evaluator.ScoreResult, threshold float64) status.EvalStatus {
	if score.Status != nil {
		return *score.Status
	}
	if score.Score < threshold {
		return status.EvalStatusFailed
	}
	return status.EvalStatusPassed
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
	actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	constructor, ok := r.LLMEvaluator.(messagesconstructor.StructuredOutputMessagesConstructor)
	if ok {
		return constructor.StructuredOutput(ctx, actuals, expecteds, evalMetric)
	}
	return nil, nil
}
