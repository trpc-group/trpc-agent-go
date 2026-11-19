//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type persistenceObserver struct {
	called atomic.Bool
}

func (p *persistenceObserver) ProcessResponse(context.Context, *agent.Invocation, *model.Request, *model.Response, chan<- *event.Event) {
	p.called.Store(true)
}

func TestToolCallPersistenceBeforeResponseProcessors(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		required, consumer, partial, toolCall bool
		wantWait                              bool
	}{
		{name: "acknowledged", required: true, consumer: true, toolCall: true, wantWait: true},
		{name: "no requirement", consumer: true, toolCall: true},
		{name: "standalone flow", required: true, toolCall: true},
		{name: "partial tool call", required: true, consumer: true, partial: true, toolCall: true},
		{name: "text response", required: true, consumer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			inv := agent.NewInvocation()
			if tc.required {
				barrier.RequireToolCallPersistence(inv)
			}
			if tc.consumer {
				barrier.Enable(inv)
			}
			post := &persistenceObserver{}
			f := New(nil, []flow.ResponseProcessor{post}, Options{})
			msg := model.NewAssistantMessage("Inspect the file.")
			if tc.toolCall {
				msg.ToolCalls = []model.ToolCall{{ID: "call", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}}}
			}
			rsp := &model.Response{IsPartial: tc.partial, Choices: []model.Choice{{Message: msg}}}
			out := make(chan *event.Event, 1)
			var processErr error
			p := newStreamingResponseProcessor(f, ctx, inv, nil, &model.Request{}, out,
				oteltrace.SpanFromContext(ctx), false, &processErr)
			done := make(chan bool, 1)
			go func() { done <- p.process(rsp) }()
			var evt *event.Event
			select {
			case evt = <-out:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Equal(t, tc.wantWait, evt.RequiresCompletion)
			if tc.wantWait {
				require.NoError(t, inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID)))
			}
			select {
			case ok := <-done:
				require.True(t, ok)
				require.NoError(t, processErr)
				require.True(t, post.called.Load())
			case <-time.After(5 * time.Second):
				t.Fatal("response processing did not finish")
			}
		})
	}
}

// A buffered event channel lets emission finish without a consumer. No
// acknowledgement is ever sent: processing must terminate with a wait error,
// never reach the response processor. The deadline bounds the test, rather
// than providing a scheduling window for a negative assertion. Removing the
// wait therefore fails this test regardless of goroutine scheduling.
func TestToolCallPersistenceWithoutAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inv := agent.NewInvocation()
	barrier.Enable(inv)
	barrier.RequireToolCallPersistence(inv)
	post := &persistenceObserver{}
	f := New(nil, []flow.ResponseProcessor{post}, Options{})
	msg := model.NewAssistantMessage("Inspect the file.")
	msg.ToolCalls = []model.ToolCall{{ID: "call", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}}}
	rsp := &model.Response{Choices: []model.Choice{{Message: msg}}}
	out := make(chan *event.Event, 1)
	var processErr error
	p := newStreamingResponseProcessor(f, ctx, inv, nil, &model.Request{}, out,
		oteltrace.SpanFromContext(ctx), false, &processErr)
	require.False(t, p.process(rsp))
	require.ErrorContains(t, processErr, "wait for tool-call event persistence")
	require.False(t, post.called.Load(), "processors must not run without acknowledgement")
	require.Len(t, out, 1, "the response must have been emitted before waiting")
	require.True(t, (<-out).RequiresCompletion)
}

func TestToolCallPersistenceCanceledAfterEmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inv := agent.NewInvocation()
	barrier.Enable(inv)
	barrier.RequireToolCallPersistence(inv)
	post := &persistenceObserver{}
	f := New(nil, []flow.ResponseProcessor{post}, Options{})
	msg := model.NewAssistantMessage("Inspect the file.")
	msg.ToolCalls = []model.ToolCall{{ID: "call", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}}}
	out := make(chan *event.Event, 1)
	// Cancellation is triggered by emission; no acknowledgement can race with it.
	canceled := make(chan struct{})
	go func() {
		defer close(canceled)
		select {
		case <-out:
			cancel()
		case <-ctx.Done():
		}
	}()
	var processErr error
	p := newStreamingResponseProcessor(f, ctx, inv, nil, &model.Request{}, out,
		oteltrace.SpanFromContext(ctx), false, &processErr)
	ok := p.process(&model.Response{Choices: []model.Choice{{Message: msg}}})
	cancel()
	<-canceled
	require.False(t, ok)
	require.ErrorIs(t, processErr, context.Canceled)
	require.ErrorContains(t, processErr, "wait for tool-call event persistence")
	require.False(t, post.called.Load())
}
