//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type enqueueRequestProcessor struct{ queue *steer.Queue }

func (p enqueueRequestProcessor) ProcessRequest(_ context.Context, _ *agent.Invocation, req *model.Request, _ chan<- *event.Event) {
	req.Messages = []model.Message{model.NewUserMessage("Q1")}
	p.queue.Enqueue(model.NewUserMessage("Q2"))
}
func TestInterruptInputDuringPreprocess(t *testing.T) {
	inv := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(inv, q)
	steer.EnableInterrupt(inv)
	f := New([]flow.RequestProcessor{enqueueRequestProcessor{q}}, nil, Options{})
	// A nil model would fail if inference were reached with the stale request.
	evt, err := f.runOneStep(context.Background(), inv, make(chan *event.Event, 4))
	require.Nil(t, evt)
	require.ErrorIs(t, err, errQueuedInput)
	require.Len(t, q.Drain(), 1)
}
func TestInterruptContextsPreserveRunCauseAndCallbackValues(t *testing.T) {
	type key struct{}
	run, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)
	modelCtx, cancelModel := context.WithCancel(run)
	values := context.WithValue(modelCtx, key{}, "callback")
	tools := contextWithRunLifetime(run, values)
	cancelModel()
	require.NoError(t, tools.Err())
	require.Nil(t, context.Cause(tools))
	require.Equal(t, "callback", tools.Value(key{}))
	cause := errors.New("caller stopped")
	cancelRun(cause)
	require.ErrorIs(t, tools.Err(), context.Canceled)
	require.ErrorIs(t, context.Cause(tools), cause)
}

func TestInterruptCycleNotInheritedByChildFlow(t *testing.T) {
	parent := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(parent, q)
	steer.EnableInterrupt(parent)
	parentCtx, cycle, err := beginModelCycle(context.Background(), parent)
	require.NoError(t, err)
	defer cycle.control.End()
	defer cycle.cancel()
	child := agent.NewInvocation()
	child.Model = &mockModel{responses: []*model.Response{{ID: "child", Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}}}
	f := New([]flow.RequestProcessor{&seedMessagesRequestProcessor{messages: []model.Message{model.NewUserMessage("child input")}}}, nil, Options{})
	events, err := f.Run(parentCtx, child)
	require.NoError(t, err)
	for evt := range events {
		if evt.RequiresCompletion {
			child.NotifyCompletion(context.Background(), agent.GetAppendEventNoticeKey(evt.ID))
		}
	}
	require.Empty(t, cycle.responseID)
	require.Empty(t, cycle.cleanup)
	require.True(t, q.Enqueue(model.NewUserMessage("parent input")))
	require.True(t, cycle.control.Interrupted(), "child completion must not accept the parent's response")
}

func TestInterruptDoesNotHideUnrelatedResponseError(t *testing.T) {
	inv := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(inv, q)
	steer.EnableInterrupt(inv)
	_, cycle, err := beginModelCycle(context.Background(), inv)
	require.NoError(t, err)
	defer cycle.cancel()
	defer cycle.control.End()
	require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
	for _, responseError := range []*model.ResponseError{
		{Type: model.ErrorTypeAPIError, Message: "database failed (context canceled)"},
		{Type: model.ErrorTypeCancelled, Message: context.DeadlineExceeded.Error()},
	} {
		var result error
		p := &streamingResponseProcessor{cycle: cycle, err: &result}
		require.True(t, p.stopForInterruptedResponse(&model.Response{Error: responseError}))
		require.ErrorContains(t, result, responseError.Message)
	}
	var result error
	p := &streamingResponseProcessor{cycle: cycle, err: &result}
	require.True(t, p.stopForInterruptedResponse(&model.Response{Error: &model.ResponseError{Type: model.ErrorTypeStreamError, Message: "stream: context canceled"}}))
	require.NoError(t, result)
}
