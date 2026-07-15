//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestModelCallBudgetModel_EnforcesPerContextLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 2)

	_, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (2) exceeded")
	require.EqualValues(t, 2, underlying.callCount())
}

func TestModelCallBudgetModel_NoBudgetPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)

	_, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetIterModel_NoBudgetPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	seq, err := iter.GenerateContentIter(context.Background(), &model.Request{})
	require.NoError(t, err)

	var responses int
	seq(func(*model.Response) bool {
		responses++
		return true
	})
	require.Equal(t, 1, responses)
	require.EqualValues(t, 1, underlying.iterCallCount())
}

func TestModelCallBudgetModel_UsesInvocationRuntimeStateFactory(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	factory := newModelCallBudgetFactory(1, false)
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: factory,
		})),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetIterModel_EnforcesPerContextLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	ctx := withModelCallBudget(context.Background(), 1)
	_, err := iter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = iter.GenerateContentIter(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.iterCallCount())
}

func TestModelCallBudgetModel_ConcurrentCallsShareLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 3)

	var wg sync.WaitGroup
	var successes atomic.Int64
	var failures atomic.Int64
	errs := make(chan string, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := wrapped.GenerateContent(ctx, &model.Request{})
			if err != nil {
				stopErr, ok := agent.AsStopError(err)
				if !ok || stopErr.Message !=
					"max LLM calls (3) exceeded" {
					errs <- err.Error()
				}
				failures.Add(1)
				return
			}
			successes.Add(1)
		}()
	}
	wg.Wait()
	close(errs)

	for msg := range errs {
		require.Empty(t, msg)
	}
	require.EqualValues(t, 3, successes.Load())
	require.EqualValues(t, 13, failures.Load())
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetModel_InfoPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)

	require.Equal(t, underlying.Info(), wrapped.Info())
}

func TestNewModelCallBudgetModel_Nil(t *testing.T) {
	t.Parallel()

	require.Nil(t, newModelCallBudgetModel(nil))
}

func TestModelCallBudget_Guards(t *testing.T) {
	t.Parallel()

	require.Nil(t, newModelCallBudget(0))
	require.Nil(t, newModelCallBudget(-1))
	_, err := (*modelCallBudget)(nil).use()
	require.NoError(t, err)

	ctx := withModelCallBudget(nil, 1)
	require.NotNil(t, ctx)
	require.NotNil(t, modelCallBudgetFromContext(ctx))

	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: "not-a-budget",
		})),
	))
	ctx = agent.NewInvocationContext(context.Background(), inv)
	require.Nil(t, modelCallBudgetFromContext(ctx))
}

func TestModelCallBudgetCallbacks_RunBeforeModel(t *testing.T) {
	t.Parallel()

	callbacks := modelCallBudgetCallbacks()
	require.NotNil(t, callbacks)
	require.Len(t, callbacks.BeforeModel, 1)

	result, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestBaseLLMAgentOptions_AddsModelCallBudgetCallbacks(t *testing.T) {
	t.Parallel()

	withoutBudget := baseLLMAgentOptions(
		&countingBudgetModel{},
		agentConfig{},
		"",
		"",
		model.GenerationConfig{},
		nil,
	)
	withBudget := baseLLMAgentOptions(
		&countingBudgetModel{},
		agentConfig{MaxLLMCalls: 1},
		"",
		"",
		model.GenerationConfig{},
		nil,
	)

	require.Len(t, withBudget, len(withoutBudget)+1)
}

func TestAppendModelCallBudgetGatewayOption_Disabled(t *testing.T) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 0, false)
	require.Empty(t, opts)
}

func TestAppendModelCallBudgetGatewayOption_AddsRunBudget(t *testing.T) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 1, false)
	require.Len(t, opts, 1)
}

func TestModelCallBudgetModel_FinalizesOnLastAllowedCall(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(1, true),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		ExtraFields: map[string]any{
			"parallel_tool_calls": true,
			"response_format":     "json",
			"tool_choice":         "required",
			"tools":               []string{"search"},
		},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
	require.Contains(
		t,
		got.Messages[1].Content,
		"Do not emit tool calls",
	)
	require.Contains(
		t,
		got.Messages[1].Content,
		"<tool_call>",
	)
	require.Len(t, req.Tools, 1)
	require.Len(t, req.Messages, 1)
	require.Equal(t, map[string]any{
		"parallel_tool_calls": true,
		"response_format":     "json",
		"tool_choice":         "required",
		"tools":               []string{"search"},
	}, req.ExtraFields)
	require.Equal(t, map[string]any{
		"response_format": "json",
	}, got.ExtraFields)
}

func TestModelCallBudgetModel_UserPromptPrefixConsumesBudget(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 1)
	req := &model.Request{Messages: []model.Message{model.NewUserMessage(
		"Analyze the following conversation between a user and an assistant, " +
			"and provide a concise summary.",
	)}}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetBypassModel_DoesNotConsumeContextBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	ctx := withModelCallBudget(context.Background(), 1)

	_, err := bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetBypassModel_DoesNotConsumeInvocationBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	factory := newModelCallBudgetFactory(1, false)
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: factory,
		})),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetBypassIterModel_DoesNotConsumeContextBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	bypassIter, ok := bypassed.(model.IterModel)
	require.True(t, ok)
	budgetedIter, ok := budgeted.(model.IterModel)
	require.True(t, ok)
	ctx := withModelCallBudget(context.Background(), 1)

	_, err := bypassIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgetedIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgetedIter.GenerateContentIter(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.iterCallCount())
}

type countingBudgetModel struct {
	calls atomic.Int64
}

func (m *countingBudgetModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.calls.Add(1)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage("ok"),
	}}}
	close(ch)
	return ch, nil
}

func (m *countingBudgetModel) Info() model.Info {
	return model.Info{Name: "counting"}
}

func (m *countingBudgetModel) callCount() int64 {
	return m.calls.Load()
}

type capturingBudgetModel struct {
	mu   sync.Mutex
	last *model.Request
}

func (m *capturingBudgetModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.last = req
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{}
	close(ch)
	return ch, nil
}

func (m *capturingBudgetModel) Info() model.Info {
	return model.Info{Name: "capturing"}
}

func (m *capturingBudgetModel) lastRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

type countingBudgetIterModel struct {
	countingBudgetModel
	iterCalls atomic.Int64
}

func (m *countingBudgetIterModel) GenerateContentIter(
	_ context.Context,
	_ *model.Request,
) (model.Seq[*model.Response], error) {
	m.iterCalls.Add(1)
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{})
	}, nil
}

func (m *countingBudgetIterModel) iterCallCount() int64 {
	return m.iterCalls.Load()
}

func TestBuildModelCallBudgetRunOptionResolverInjectsBudget(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(1, false)
	ctx, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 1)

	require.Nil(t, modelCallBudgetFromContext(ctx))

	opts := agent.NewRunOptions(runOpts...)
	rawFactory := opts.RuntimeState[modelCallBudgetRuntimeStateKey]
	factory, ok := rawFactory.(*modelCallBudgetFactory)
	require.True(t, ok)
	require.NotNil(t, factory)

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	parent := agent.NewInvocation(
		agent.WithInvocationID("parent"),
		agent.WithInvocationRunOptions(opts),
	)
	parentCtx := agent.NewInvocationContext(context.Background(), parent)
	child := parent.Clone(agent.WithInvocationID("child"))
	childCtx := agent.NewInvocationContext(context.Background(), child)

	_, err = wrapped.GenerateContent(parentCtx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(parentCtx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")

	_, err = wrapped.GenerateContent(childCtx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(childCtx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 2, underlying.callCount())
}

func TestBuildModelCallBudgetRunOptionResolverBypassesAuxiliaryCalls(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(1, false)
	_, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)

	opts := agent.NewRunOptions(runOpts...)
	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	auxiliary := newModelCallBudgetBypassModel(budgeted)
	inv := agent.NewInvocation(
		agent.WithInvocationID("run"),
		agent.WithInvocationRunOptions(opts),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err = auxiliary.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = auxiliary.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}
