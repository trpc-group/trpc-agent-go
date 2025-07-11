//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package llmflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent implements agent.Agent for testing
type mockAgent struct {
	name  string
	tools []tool.CallableTool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Simple mock implementation
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.CallableTool {
	return m.tools
}

// mockModel implements model.Model for testing
type mockModel struct {
	ShouldError bool
	responses   []*model.Response
	currentIdx  int
}

func (m *mockModel) Info() model.Info {
	return model.Info{
		Name: "mock",
	}
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.ShouldError {
		return nil, errors.New("mock model error")
	}

	respChan := make(chan *model.Response, len(m.responses))

	go func() {
		defer close(respChan)
		for _, resp := range m.responses {
			select {
			case respChan <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()

	return respChan, nil
}

// mockRequestProcessor implements flow.RequestProcessor
type mockRequestProcessor struct{}

func (m *mockRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "preprocessing"
	select {
	case ch <- evt:
	default:
	}
}

// mockResponseProcessor implements flow.ResponseProcessor
type mockResponseProcessor struct{}

func (m *mockResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "postprocessing"
	select {
	case ch <- evt:
	default:
	}
}

func TestFlow_Interface(t *testing.T) {
	llmFlow := New(nil, nil, Options{})
	var f flow.Flow = llmFlow

	// Test that the flow implements the interface
	log.Debugf("Flow interface test: %v", f)

	// Simple compile test
	var _ flow.Flow = f
}

func TestModelCallbacks_BeforeSkip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "skip-response"}, nil // Return custom response to skip model call
	})

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "skip-response", events[0].Response.ID)
}

func TestModelCBs_BeforeCustom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "custom-before"}, nil
	})

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "custom-before", events[0].Response.ID)
}

func TestModelCallbacks_BeforeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, errors.New("before error")
	})

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "before error" {
			cancel()
			break
		}
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "before error", events[0].Error.Message)
}

func TestModelCBs_AfterOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, rsp *model.Response, modelErr error) (*model.Response, error) {
			return &model.Response{Object: "after-override"}, nil
		},
	)

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "original"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first event and cancel ctx to prevent deadlock.
		cancel()
		break
	}
	require.Equal(t, 1, len(events))
	t.Log(events[0])
	require.Equal(t, "after-override", events[0].Response.Object)
}

func TestModelCallbacks_AfterError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, rsp *model.Response, modelErr error) (*model.Response, error) {
			return nil, errors.New("after error")
		},
	)

	llmFlow := New(nil, nil, Options{})
	invocation := &agent.Invocation{
		InvocationID:   "test-invocation",
		AgentName:      "test-agent",
		ModelCallbacks: modelCallbacks,
		Model: &mockModel{
			responses: []*model.Response{{ID: "original"}},
		},
		Session: &session.Session{
			ID: "test-session",
		},
	}
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "after error" {
			cancel()
			break
		}
	}
	require.Equal(t, 1, len(events))
	require.Equal(t, "after error", events[0].Error.Message)
}
