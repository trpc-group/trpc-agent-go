//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
	llmtemplate "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/template"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubRunner struct{}

func (stubRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	return nil, nil
}

func (stubRunner) Close() error {
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

func (stubEvalCaseResultAggregator) Aggregate(context.Context, *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error) {
	return &EvalCaseResultAggregationResult{}, nil
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()
	assert.NotNil(t, opts.EvalSetManager)
	assert.NotNil(t, opts.EvalResultManager)
	assert.NotNil(t, opts.Registry)
	assert.NotNil(t, opts.MetricRegistry)
	assert.NotNil(t, opts.EvalCaseResultAggregator)
	assert.NotNil(t, opts.SessionIDSupplier)
	assert.Nil(t, opts.ExpectedRunner)
	assert.Nil(t, opts.UserSimulator)
	assert.Nil(t, opts.Callbacks)
	assert.Equal(t, runtime.GOMAXPROCS(0), opts.EvalCaseParallelism)
	assert.False(t, opts.EvalCaseParallelInferenceEnabled)
	assert.False(t, opts.EvalCaseParallelEvaluationEnabled)
	sessionID := opts.SessionIDSupplier(context.Background())
	assert.NotEmpty(t, sessionID)
}

func TestWithEvalSetManager(t *testing.T) {
	custom := evalsetinmemory.New()
	opts := NewOptions(WithEvalSetManager(custom))
	assert.Equal(t, custom, opts.EvalSetManager)
}

func TestWithEvalResultManager(t *testing.T) {
	custom := evalresultinmemory.New()
	opts := NewOptions(WithEvalResultManager(custom))
	assert.Equal(t, custom, opts.EvalResultManager)
}

func TestWithRegistry(t *testing.T) {
	custom := registry.New()
	opts := NewOptions(WithRegistry(custom))
	assert.Equal(t, custom, opts.Registry)
}

func TestWithLLMOperatorRegistry(t *testing.T) {
	operatorRegistry := operatorregistry.New()
	err := operatorRegistry.RegisterResponseScorer("service_score", serviceTemplateScorer{})
	assert.NoError(t, err)
	opts := NewOptions(WithLLMOperatorRegistry(operatorRegistry))
	templateEval, err := opts.Registry.Get(llmtemplate.EvaluatorName)
	assert.NoError(t, err)
	scorer, ok := templateEval.(interface {
		ScoreBasedOnResponse(context.Context, *model.Response, *metric.EvalMetric) (*evaluator.ScoreResult, error)
	})
	assert.True(t, ok)
	result, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{}, serviceTemplateMetric("service_score"))
	assert.NoError(t, err)
	assert.Equal(t, 0.8, result.Score)
}

func TestWithRegistryOverridesLLMOperatorRegistryWhenLast(t *testing.T) {
	custom := registry.New()
	opts := NewOptions(WithLLMOperatorRegistry(operatorregistry.New()), WithRegistry(custom))
	assert.Equal(t, custom, opts.Registry)
}

func TestWithMetricRegistry(t *testing.T) {
	custom := metricregistry.New()
	opts := NewOptions(WithMetricRegistry(custom))
	assert.Equal(t, custom, opts.MetricRegistry)
}

func TestWithEvalCaseResultAggregator(t *testing.T) {
	custom := stubEvalCaseResultAggregator{}
	opts := NewOptions(WithEvalCaseResultAggregator(custom))
	assert.Equal(t, custom, opts.EvalCaseResultAggregator)
}

func TestWithSessionIDSupplier(t *testing.T) {
	called := false
	supplier := func(ctx context.Context) string {
		called = true
		return "session-custom"
	}
	opts := NewOptions(WithSessionIDSupplier(supplier))
	assert.Equal(t, "session-custom", opts.SessionIDSupplier(context.Background()))
	assert.True(t, called)
}

func TestWithUserSimulator(t *testing.T) {
	custom := stubSimulator{}
	opts := NewOptions(WithUserSimulator(custom))
	assert.Equal(t, custom, opts.UserSimulator)
}

func TestWithCallbacks(t *testing.T) {
	callbacks := &Callbacks{}
	opts := NewOptions(WithCallbacks(callbacks))
	assert.Same(t, callbacks, opts.Callbacks)
}

func TestWithExpectedRunner(t *testing.T) {
	custom := stubRunner{}
	opts := NewOptions(WithExpectedRunner(custom))
	assert.Equal(t, custom, opts.ExpectedRunner)
}

func TestWithToolMockRunner(t *testing.T) {
	custom := stubRunner{}
	opts := NewOptions(WithToolMockRunner(custom))
	assert.Equal(t, custom, opts.ToolMockRunner)
}

func TestWithRunOptions(t *testing.T) {
	opts := NewOptions(WithRunOptions(agent.WithInstruction("prompt")))
	assert.Len(t, opts.RunOptions, 1)
}

func TestWithEvalCaseParallelism(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelism(3))
	assert.Equal(t, 3, opts.EvalCaseParallelism)
}

func TestWithEvalCaseParallelInferenceEnabled(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelInferenceEnabled(true))
	assert.True(t, opts.EvalCaseParallelInferenceEnabled)
}

func TestWithEvalCaseParallelEvaluationEnabled(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelEvaluationEnabled(true))
	assert.True(t, opts.EvalCaseParallelEvaluationEnabled)
}

type serviceTemplateScorer struct{}

func (serviceTemplateScorer) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return &evaluator.ScoreResult{
		Score:  0.8,
		Reason: "service scorer",
	}, nil
}

func serviceTemplateMetric(responseScorerName string) *metric.EvalMetric {
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
