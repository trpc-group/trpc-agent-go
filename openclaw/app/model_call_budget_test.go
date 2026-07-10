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
	"time"

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
	factory := newModelCallBudgetFactory(1, false, 0)
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

	require.Nil(t, newModelCallBudget(0, false, 0))
	require.Nil(t, newModelCallBudget(-1, false, 0))
	require.NotNil(t, newModelCallBudget(0, false, time.Second))
	_, err := (*modelCallBudget)(nil).use(context.Background())
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

func TestModelCallBudgetDeadlineSoon_Guards(t *testing.T) {
	t.Parallel()

	require.False(t, modelCallBudgetDeadlineSoon(nil, time.Second))
	require.False(
		t,
		modelCallBudgetDeadlineSoon(context.Background(), time.Second),
	)

	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	require.False(t, modelCallBudgetDeadlineSoon(ctx, 0))
	require.True(t, modelCallBudgetDeadlineSoon(ctx, time.Minute))
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

	opts := appendModelCallBudgetGatewayOption(nil, 0, false, 0)
	require.Empty(t, opts)
}

func TestAppendModelCallBudgetGatewayOption_AddsRunBudget(t *testing.T) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 1, false, 0)
	require.Len(t, opts, 1)
}

func TestAppendModelCallBudgetGatewayOption_AddsDeadlineBudget(
	t *testing.T,
) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 0, false, time.Minute)
	require.Len(t, opts, 1)
}

func TestModelCallBudgetRunOptions(t *testing.T) {
	t.Parallel()

	require.Nil(t, modelCallBudgetRunOptions(0, false, 0))

	runOpts := modelCallBudgetRunOptions(0, false, time.Minute)
	require.Len(t, runOpts, 1)
	opts := agent.NewRunOptions(runOpts...)
	factory, ok := opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.Equal(t, time.Minute, factory.deadlineWindow)
}

func TestModelCallBudgetModel_FinalizesOnLastAllowedCall(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(1, true, 0),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
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
	require.False(t, got.Stream)
	require.True(t, req.Stream)
}

func TestModelCallBudgetModel_FinalizationDisablesThinking(
	t *testing.T,
) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(
			1,
			true,
			0,
			modelCallBudgetFinalRequestConfig{DisableThinking: true},
		),
	)
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			ThinkingEnabled: model.BoolPtr(true),
		},
		Messages: []model.Message{model.NewUserMessage("question")},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.NotNil(t, got.ThinkingEnabled)
	require.False(t, *got.ThinkingEnabled)
	require.NotNil(t, req.ThinkingEnabled)
	require.True(t, *req.ThinkingEnabled)
}

func TestModelCallBudgetModel_FinalizesNearDeadline(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
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
	require.Len(t, req.Tools, 1)
	require.Len(t, req.Messages, 1)
	require.False(t, got.Stream)
	require.True(t, req.Stream)
}

func TestModelCallBudgetIterModel_FinalizesNearDeadline(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	_, err := iter.GenerateContentIter(ctx, req)
	require.NoError(t, err)

	got := underlying.lastIterRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
	require.Len(t, req.Tools, 1)
	require.Len(t, req.Messages, 1)
	require.False(t, got.Stream)
	require.True(t, req.Stream)
}

func TestModelCallBudgetModel_FinalizesWhenPrefinalWindowExpires(
	t *testing.T,
) {
	t.Parallel()

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(200*time.Millisecond),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 150*time.Millisecond),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	ch, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	for resp := range ch {
		got = append(got, resp)
	}

	require.Len(t, got, 1)
	require.Equal(t, "final answer", got[0].Choices[0].Message.Content)
	requests := underlying.requestsSnapshot()
	require.Len(t, requests, 2)
	require.NotNil(t, requests[0].Tools)
	require.Nil(t, requests[1].Tools)
	require.Len(t, requests[1].Messages, 2)
	require.Contains(
		t,
		requests[1].Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
}

func TestModelCallBudgetIterModel_FinalizesWhenPrefinalWindowExpires(
	t *testing.T,
) {
	t.Parallel()

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(200*time.Millisecond),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 150*time.Millisecond),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	seq, err := iter.GenerateContentIter(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	seq(func(resp *model.Response) bool {
		got = append(got, resp)
		return true
	})

	require.Len(t, got, 1)
	require.Equal(t, "final answer", got[0].Choices[0].Message.Content)
	requests := underlying.iterRequestsSnapshot()
	require.Len(t, requests, 2)
	require.NotNil(t, requests[0].Tools)
	require.Nil(t, requests[1].Tools)
	require.Len(t, requests[1].Messages, 2)
	require.Contains(
		t,
		requests[1].Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
}

func TestModelCallBudgetModel_DoesNotFinalizeOutsideDeadlineWindow(
	t *testing.T,
) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Hour),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.NotNil(t, got.Tools)
	require.Len(t, got.Messages, 1)
	require.NotNil(t, req.Tools)
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
	factory := newModelCallBudgetFactory(1, false, 0)
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
	mu       sync.Mutex
	last     *model.Request
	iterLast *model.Request
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

func (m *capturingBudgetModel) GenerateContentIter(
	_ context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterLast = req
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{})
	}, nil
}

func (m *capturingBudgetModel) lastRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func (m *capturingBudgetModel) lastIterRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.iterLast
}

type prefinalTimeoutBudgetModel struct {
	mu           sync.Mutex
	requests     []*model.Request
	iterRequests []*model.Request
}

func (m *prefinalTimeoutBudgetModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		if req == nil || req.Tools == nil {
			ch <- modelCallBudgetTestFinalResponse()
			return
		}
		<-ctx.Done()
		ch <- &model.Response{
			Error: model.ResponseErrorFromError(
				ctx.Err(),
				model.ErrorTypeStreamError,
			),
			Done: true,
		}
	}()
	return ch, nil
}

func (m *prefinalTimeoutBudgetModel) Info() model.Info {
	return model.Info{Name: "prefinal-timeout"}
}

func (m *prefinalTimeoutBudgetModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterRequests = append(m.iterRequests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		if req == nil || req.Tools == nil {
			yield(modelCallBudgetTestFinalResponse())
			return
		}
		<-ctx.Done()
		yield(&model.Response{
			Error: model.ResponseErrorFromError(
				ctx.Err(),
				model.ErrorTypeStreamError,
			),
			Done: true,
		})
	}, nil
}

func (m *prefinalTimeoutBudgetModel) requestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func (m *prefinalTimeoutBudgetModel) iterRequestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.iterRequests...)
}

func modelCallBudgetTestFinalResponse() *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("final answer"),
		}},
	}
}

func cloneBudgetTestRequest(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}
	clone := *req
	if req.Tools != nil {
		clone.Tools = make(map[string]tool.Tool, len(req.Tools))
		for name, t := range req.Tools {
			clone.Tools[name] = t
		}
	}
	clone.Messages = append([]model.Message(nil), req.Messages...)
	return &clone
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

	resolver := buildModelCallBudgetRunOptionResolver(1, false, 0)
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

	resolver := buildModelCallBudgetRunOptionResolver(1, false, 0)
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

func TestBuildModelCallBudgetRunOptionResolverInjectsDeadlineBudget(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(
		0,
		false,
		time.Minute,
	)
	ctx, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 1)
	require.Nil(t, modelCallBudgetFromContext(ctx))

	opts := agent.NewRunOptions(runOpts...)
	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	inv := agent.NewInvocation(
		agent.WithInvocationID("deadline-run"),
		agent.WithInvocationRunOptions(opts),
	)
	deadlineCtx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	deadlineCtx = agent.NewInvocationContext(deadlineCtx, inv)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	_, err = wrapped.GenerateContent(deadlineCtx, req)
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
}

func TestModelCallBudgetFactoryReusesDeadlineBudgetForInvocation(
	t *testing.T,
) {
	t.Parallel()

	factory := newModelCallBudgetFactory(0, false, time.Minute)
	inv := agent.NewInvocation(agent.WithInvocationID("deadline-run"))

	first := factory.budgetFor(inv)
	second := factory.budgetFor(inv)

	require.NotNil(t, first)
	require.Same(t, first, second)
	require.Equal(t, time.Minute, first.deadlineWindow)
}
