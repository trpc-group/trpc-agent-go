//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// endInvokingProcessor ends the invocation immediately and emits one event.
type endInvokingProcessor struct{}

func (p *endInvokingProcessor) ProcessRequest(ctx context.Context, inv *agent.Invocation, req *model.Request, ch chan<- *event.Event) {
	inv.EndInvocation = true
	e := event.New(inv.InvocationID, inv.AgentName)
	e.Object = "preprocess.end"
	ch <- e
}

// shouldNotRunProcessor records if it is invoked.
type shouldNotRunProcessor struct{ called *bool }

func (p *shouldNotRunProcessor) ProcessRequest(ctx context.Context, inv *agent.Invocation, req *model.Request, ch chan<- *event.Event) {
	if p.called != nil {
		*p.called = true
	}
	e := event.New(inv.InvocationID, inv.AgentName)
	e.Object = "preprocess.should_not_run"
	ch <- e
}

func TestPreprocess_StopsAfterEndInvocation(t *testing.T) {
	// Arrange: first processor ends the invocation, second also should be run.
	var called bool
	reqProcs := []flow.RequestProcessor{
		&endInvokingProcessor{},
		&shouldNotRunProcessor{called: &called},
	}

	f := New(reqProcs, nil, Options{})
	inv := agent.NewInvocation()

	// Act
	ch, err := f.Run(context.Background(), inv)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	// Assert
	require.True(t, called, "subsequent processors should run after EndInvocation")
	require.Len(t, events, 3)
	require.Equal(t, "preprocess.end", events[1].Object)
}

// twoChunkModel returns two streaming chunks to ensure we break after EndInvocation.
type twoChunkModel struct{}

func (m *twoChunkModel) Info() model.Info { return model.Info{Name: "mock"} }

func (m *twoChunkModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 2)
	go func() {
		defer close(ch)
		ch <- &model.Response{
			ID:        "1",
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "a"}}},
			IsPartial: true,
		}
		ch <- &model.Response{
			ID:        "2",
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "b"}}},
			IsPartial: true,
		}
	}()
	return ch, nil
}

// endOnFirstChunkProcessor sets EndInvocation on the first response.
type endOnFirstChunkProcessor struct{ done bool }

func (p *endOnFirstChunkProcessor) ProcessResponse(ctx context.Context, inv *agent.Invocation, req *model.Request, rsp *model.Response, ch chan<- *event.Event) {
	if !p.done {
		inv.EndInvocation = true
		p.done = true
	}
}

func TestStreaming_BreaksWhenEndInvocationSet(t *testing.T) {
	// Arrange: model returns two chunks; response processor ends invocation on first chunk.
	respProcs := []flow.ResponseProcessor{&endOnFirstChunkProcessor{}}
	f := New(nil, respProcs, Options{})
	inv := &agent.Invocation{InvocationID: "inv-stream", AgentName: "agent-stream", Model: &twoChunkModel{}}

	// Act
	ch, err := f.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events authored by the LLM chunks.
	var chunkCount int
	for e := range ch {
		if e.Response != nil && e.Response.Object == model.ObjectTypeChatCompletionChunk {
			chunkCount++
		}
	}

	// Assert: only the first chunk should be observed.
	require.Equal(t, 2, chunkCount)
}
