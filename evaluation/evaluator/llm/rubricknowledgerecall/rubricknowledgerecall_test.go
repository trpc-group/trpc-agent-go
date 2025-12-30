//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubricknowledgerecall

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubMessagesConstructor struct {
	called bool
}

func (s *stubMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	_ *metric.EvalMetric) ([]model.Message, error) {
	s.called = true
	return []model.Message{{Role: model.RoleUser, Content: actuals[0].InvocationID + expecteds[0].InvocationID}}, nil
}

type stubResponseScorer struct {
	called bool
}

func (s *stubResponseScorer) ScoreBasedOnResponse(ctx context.Context, _ *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	s.called = true
	return &evaluator.ScoreResult{Score: 1, RubricScores: nil}, nil
}

type stubSamplesAggregator struct {
	called bool
}

func (s *stubSamplesAggregator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	s.called = true
	return samples[0], nil
}

type stubInvocationsAggregator struct {
	called bool
}

func (s *stubInvocationsAggregator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	s.called = true
	return &evaluator.EvaluateResult{OverallStatus: results[0].Status, PerInvocationResults: results}, nil
}

type stubLLMBase struct {
	evaluateCalled bool
	result         *evaluator.EvaluateResult
}

func (s *stubLLMBase) Name() string { return "stub" }

func (s *stubLLMBase) Description() string { return "stub" }

func (s *stubLLMBase) Evaluate(_ context.Context, _ []*evalset.Invocation, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	s.evaluateCalled = true
	return s.result, nil
}

func (s *stubLLMBase) ConstructMessages(context.Context, []*evalset.Invocation, []*evalset.Invocation,
	*metric.EvalMetric) ([]model.Message, error) {
	return nil, nil
}

func (s *stubLLMBase) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return nil, nil
}

func (s *stubLLMBase) AggregateSamples(context.Context, []*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return nil, nil
}

func (s *stubLLMBase) AggregateInvocations(context.Context, []*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return s.result, nil
}

func TestRubricKnowledgeRecallEvaluatorDelegates(t *testing.T) {
	ctx := context.Background()
	mc := &stubMessagesConstructor{}
	rs := &stubResponseScorer{}
	sa := &stubSamplesAggregator{}
	ia := &stubInvocationsAggregator{}
	ev := New(
		WithMessagesConstructor(mc),
		WithResponsescorer(rs),
		WithSamplesAggregator(sa),
		WithInvocationsAggregator(ia),
	)
	impl, ok := ev.(*rubricKnowledgeRecallEvaluator)
	require.True(t, ok)

	base := &stubLLMBase{result: &evaluator.EvaluateResult{OverallStatus: status.EvalStatusPassed}}
	impl.llmBaseEvaluator = base

	_, err := impl.ConstructMessages(ctx, []*evalset.Invocation{{InvocationID: "a"}}, []*evalset.Invocation{{InvocationID: "b"}}, nil)
	require.NoError(t, err)
	_, err = impl.ScoreBasedOnResponse(ctx, &model.Response{}, nil)
	require.NoError(t, err)
	_, err = impl.AggregateSamples(ctx, []*evaluator.PerInvocationResult{{Status: status.EvalStatusPassed}}, nil)
	require.NoError(t, err)
	_, err = impl.AggregateInvocations(ctx, []*evaluator.PerInvocationResult{{Status: status.EvalStatusPassed}}, nil)
	require.NoError(t, err)

	result, err := impl.Evaluate(ctx, nil, nil, nil)
	require.NoError(t, err)

	assert.True(t, mc.called)
	assert.True(t, rs.called)
	assert.True(t, sa.called)
	assert.True(t, ia.called)
	assert.True(t, base.evaluateCalled)
	assert.Equal(t, "llm_rubric_knowledge_recall", impl.Name())
	assert.Equal(t, "LLM rubric knowledge recall evaluator", impl.Description())
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
}
