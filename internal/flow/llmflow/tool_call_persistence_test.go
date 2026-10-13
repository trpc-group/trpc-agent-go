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
	called                atomic.Bool
	acknowledged          *atomic.Bool
	beforeAcknowledgement atomic.Bool
}

func (p *persistenceObserver) ProcessResponse(context.Context, *agent.Invocation, *model.Request, *model.Response, chan<- *event.Event) {
	p.called.Store(true)
	p.beforeAcknowledgement.Store(!p.acknowledged.Load())
}

func TestToolCallPersistenceBeforeResponseProcessors(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		required, consumer, partial, toolCall, cancel bool
		wantWait                                      bool
	}{
		{name: "acknowledged", required: true, consumer: true, toolCall: true, wantWait: true},
		{name: "canceled before acknowledgement", required: true, consumer: true, toolCall: true, cancel: true, wantWait: true},
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
			var acknowledged atomic.Bool
			post := &persistenceObserver{acknowledged: &acknowledged}
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
				require.False(t, post.called.Load())
				if tc.cancel {
					cancel()
				} else {
					acknowledged.Store(true)
					require.NoError(t, inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID)))
				}
			}
			select {
			case ok := <-done:
				if tc.cancel {
					require.False(t, ok)
					require.ErrorIs(t, processErr, context.Canceled)
					require.False(t, post.called.Load(), "review and tool processors must not run after synchronization fails")
				} else {
					require.True(t, ok)
					require.NoError(t, processErr)
					require.True(t, post.called.Load())
					require.Equal(t, !tc.wantWait, post.beforeAcknowledgement.Load())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("response processing did not finish")
			}
		})
	}
}
