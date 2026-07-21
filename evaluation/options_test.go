//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
	llmtemplate "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/template"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubService struct{}

func (stubService) Inference(ctx context.Context, req *service.InferenceRequest, opt ...service.Option) ([]*service.InferenceResult, error) {
	return nil, nil
}

func (stubService) Evaluate(ctx context.Context, req *service.EvaluateRequest, opt ...service.Option) (*service.EvalSetRunResult, error) {
	return nil, nil
}

func (stubService) Close() error {
	return nil
}

type stubSimulator struct{}

func (stubSimulator) Start(ctx context.Context, req *usersimulation.StartRequest) (usersimulation.Conversation, error) {
	return stubConversation{}, nil
}

type stubConversation struct{}

func (stubConversation) Next(ctx context.Context, req *usersimulation.TurnRequest) (*usersimulation.Decision, error) {
	return &usersimulation.Decision{Stop: true}, nil
}

func (stubConversation) Close() error {
	return nil
}

type stubEvalCaseResultAggregator struct{}

func (stubEvalCaseResultAggregator) Aggregate(context.Context, *service.EvalCaseResultAggregationInput) (*service.EvalCaseResultAggregationResult, error) {
	return &service.EvalCaseResultAggregationResult{}, nil
}

type evaluationTemplateScorer struct{}

func (evaluationTemplateScorer) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return &evaluator.ScoreResult{
		Score:  0.85,
		Reason: "evaluation scorer",
	}, nil
}

func evaluationTemplateMetric(responseScorerName string) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					ResponseScorerName: responseScorerName,
				},
			},
		},
	}
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, defaultNumRuns, opts.numRuns)
	assert.NotNil(t, opts.evalSetManager)
	assert.NotNil(t, opts.evalResultManager)
	assert.NotNil(t, opts.metricManager)
	assert.NotNil(t, opts.registry)
	assert.NotNil(t, opts.metricRegistry)
	assert.Nil(t, opts.evalService)
	assert.Nil(t, opts.callbacks)
	assert.Nil(t, opts.evalCaseParallelism)
	assert.Nil(t, opts.evalCaseParallelInferenceEnabled)
	assert.Nil(t, opts.evalCaseParallelEvaluationEnabled)
	assert.False(t, opts.runDetailsEnabled)
}

func TestWithEvalSetManager(t *testing.T) {
	custom := evalsetinmemory.New()
	opts := newOptions(WithEvalSetManager(custom))

	assert.Equal(t, custom, opts.evalSetManager)
}

func TestWithEvalResultManager(t *testing.T) {
	custom := evalresultinmemory.New()
	opts := newOptions(WithEvalResultManager(custom))

	assert.Equal(t, custom, opts.evalResultManager)
}

func TestWithMetricManager(t *testing.T) {
	custom := metricinmemory.New()
	opts := newOptions(WithMetricManager(custom))

	assert.Equal(t, custom, opts.metricManager)
}

func TestWithRegistry(t *testing.T) {
	custom := registry.New()
	opts := newOptions(WithRegistry(custom))
	assert.Equal(t, custom, opts.registry)
}

func TestWithLLMOperatorRegistry(t *testing.T) {
	operatorRegistry := operatorregistry.New()
	err := operatorRegistry.RegisterResponseScorer("evaluation_score", evaluationTemplateScorer{})
	assert.NoError(t, err)
	opts := newOptions(WithLLMOperatorRegistry(operatorRegistry))
	templateEval, err := opts.registry.Get(llmtemplate.EvaluatorName)
	assert.NoError(t, err)
	scorer, ok := templateEval.(interface {
		ScoreBasedOnResponse(context.Context, *model.Response, *metric.EvalMetric) (*evaluator.ScoreResult, error)
	})
	assert.True(t, ok)
	result, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{}, evaluationTemplateMetric("evaluation_score"))
	assert.NoError(t, err)
	assert.Equal(t, 0.85, result.Score)
}

func TestWithRegistryOverridesLLMOperatorRegistryWhenLast(t *testing.T) {
	custom := registry.New()
	opts := newOptions(WithLLMOperatorRegistry(operatorregistry.New()), WithRegistry(custom))
	assert.Equal(t, custom, opts.registry)
}

func TestWithMetricRegistry(t *testing.T) {
	custom := metricregistry.New()
	opts := newOptions(WithMetricRegistry(custom))
	assert.Equal(t, custom, opts.metricRegistry)
}

func TestWithEvalCaseResultAggregator(t *testing.T) {
	custom := stubEvalCaseResultAggregator{}
	opts := newOptions(WithEvalCaseResultAggregator(custom))
	assert.Equal(t, custom, opts.evalCaseResultAggregator)
}

func TestWithEvaluationService(t *testing.T) {
	custom := stubService{}
	opts := newOptions(WithEvaluationService(custom))

	assert.Equal(t, custom, opts.evalService)
}

func TestWithUserSimulator(t *testing.T) {
	custom := stubSimulator{}
	opts := newOptions(WithUserSimulator(custom))
	assert.Equal(t, custom, opts.userSimulator)
}

func TestWithCallbacks(t *testing.T) {
	custom := &service.Callbacks{}
	opts := newOptions(WithCallbacks(custom))

	assert.Same(t, custom, opts.callbacks)
}

func TestWithExpectedRunner(t *testing.T) {
	custom := stubRunner{}
	opts := newOptions(WithExpectedRunner(custom))
	assert.Equal(t, custom, opts.expectedRunner)
}

func TestWithToolMockRunner(t *testing.T) {
	custom := stubRunner{}
	opts := newOptions(WithToolMockRunner(custom))
	assert.Equal(t, custom, opts.toolMockRunner)
}

func TestWithJudgeRunner(t *testing.T) {
	custom := stubRunner{}
	opts := newOptions(WithJudgeRunner(custom))
	assert.Equal(t, custom, opts.judgeRunner)
}

func TestWithJudgeRunnerNumSamples(t *testing.T) {
	opts := newOptions(WithJudgeRunnerNumSamples(3))
	assert.NotNil(t, opts.judgeRunnerNumSamples)
	assert.Equal(t, 3, *opts.judgeRunnerNumSamples)
}

func TestWithNumRuns(t *testing.T) {
	opts := newOptions(WithNumRuns(5))
	assert.Equal(t, 5, opts.numRuns)
}

func TestWithEvalCaseIDs(t *testing.T) {
	opts := newOptions(WithEvalCaseIDs("case-1", "case-2"))
	assert.Equal(t, []string{"case-1", "case-2"}, opts.evalCaseIDs)
}

func TestWithNumRunsParallelEnabled(t *testing.T) {
	opts := newOptions(WithNumRunsParallelEnabled(true))
	assert.NotNil(t, opts.numRunsParallelEnabled)
	if opts.numRunsParallelEnabled == nil {
		return
	}
	assert.True(t, *opts.numRunsParallelEnabled)
}

func TestWithEvalCaseParallelism(t *testing.T) {
	opts := newOptions(WithEvalCaseParallelism(8))
	assert.NotNil(t, opts.evalCaseParallelism)
	if opts.evalCaseParallelism == nil {
		return
	}
	assert.Equal(t, 8, *opts.evalCaseParallelism)
}

func TestWithEvalCaseParallelInferenceEnabled(t *testing.T) {
	opts := newOptions(WithEvalCaseParallelInferenceEnabled(true))
	assert.NotNil(t, opts.evalCaseParallelInferenceEnabled)
	if opts.evalCaseParallelInferenceEnabled == nil {
		return
	}
	assert.True(t, *opts.evalCaseParallelInferenceEnabled)
}

func TestWithEvalCaseParallelEvaluationEnabled(t *testing.T) {
	opts := newOptions(WithEvalCaseParallelEvaluationEnabled(true))
	assert.NotNil(t, opts.evalCaseParallelEvaluationEnabled)
	if opts.evalCaseParallelEvaluationEnabled == nil {
		return
	}
	assert.True(t, *opts.evalCaseParallelEvaluationEnabled)
}

func TestWithRunDetailsEnabled(t *testing.T) {
	opts := newOptions(WithRunDetailsEnabled(true))
	assert.True(t, opts.runDetailsEnabled)
}

func TestOptionsValidateRejectsNilOptions(t *testing.T) {
	var opts *options

	err := opts.validate(false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "options is nil")
}

func TestOptionsValidateRejectsNilRegistry(t *testing.T) {
	opts := newOptions()
	opts.registry = nil

	err := opts.validate(false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "registry is nil")
}

func TestOptionsValidateRejectsInvalidJudgeRunnerNumSamples(t *testing.T) {
	opts := newOptions(WithJudgeRunnerNumSamples(0))

	err := opts.validate(false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "judge runner num samples must be greater than 0")
}

func TestOptionsValidateRejectsNilMetricRegistry(t *testing.T) {
	opts := newOptions()
	opts.metricRegistry = nil

	err := opts.validate(false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metric registry is nil")
}

func TestOptionsValidateRejectsNilEvalServiceWhenRequired(t *testing.T) {
	opts := newOptions()

	err := opts.validate(true)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eval service is nil")
}
