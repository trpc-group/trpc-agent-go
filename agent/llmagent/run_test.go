//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// mockFlow implements flow.Flow returning predefined events.
type mockFlow struct{ done bool }

func (m *mockFlow) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		if !m.done {
			ch <- event.New(inv.InvocationID, inv.AgentName)
		}
	}()
	return ch, nil
}

func useSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
	})
	return recorder
}

func TestLLMAgent_RunWithPlugins_RecordsInvokeAgentSpan(t *testing.T) {
	recorder := useSpanRecorder(t)
	a := New(
		"agent",
		WithDescription("agent description"),
		WithInstruction("agent instruction"),
		WithGlobalInstruction("system prompt"),
	)
	a.flow = &testResponseFlow{
		response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("done"),
			}},
		},
	}
	invocation := &agent.Invocation{
		InvocationID: "id",
		Message:      model.NewUserMessage("hello"),
	}

	events, err := agent.RunWithPlugins(context.Background(), invocation, a)
	require.NoError(t, err)
	for range events {
	}

	spans := recorder.Ended()
	require.NotEmpty(t, spans)
	spanName := "invoke_agent agent"
	var invokeSpan sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() == spanName {
			invokeSpan = span
			break
		}
	}
	require.NotNil(t, invokeSpan)
	require.True(
		t,
		hasSpanAttr(
			invokeSpan.Attributes(),
			semconvtrace.KeyGenAIAgentDescription,
			"agent description",
		),
	)
	require.True(
		t,
		hasSpanAttr(
			invokeSpan.Attributes(),
			semconvtrace.KeyGenAISystemInstructions,
			"system promptagent instruction",
		),
	)
}

type testResponseFlow struct {
	response *model.Response
}

func (m *testResponseFlow) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		if m.response != nil {
			ch <- event.NewResponseEvent(inv.InvocationID, inv.AgentName, m.response)
		}
	}()
	return ch, nil
}

func hasSpanAttr(attrs []attribute.KeyValue, key string, value string) bool {
	for _, attr := range attrs {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}

func TestLLMAgent_Run_BeforeCallbackCust(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{Object: "before", Done: true}, nil
	})

	a := New("agent", WithAgentCallbacks(cb))
	// Replace flow to avoid heavy deps.
	a.flow = &mockFlow{done: true}

	evts, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.NoError(t, err)
	first := <-evts
	require.Equal(t, "before", first.Object)
}

func TestLLMAgent_Run_BeforeCallbackErr(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, context.Canceled
	})

	a := New("agent", WithAgentCallbacks(cb))
	a.flow = &mockFlow{done: true}

	_, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.Error(t, err)
}

func TestLLMAgent_Run_FlowAndAfterCb(t *testing.T) {
	after := agent.NewCallbacks()
	after.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return &model.Response{Object: "after", Done: true}, nil
	})

	a := New("agent", WithAgentCallbacks(after))
	a.flow = &mockFlow{}

	evts, err := a.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "agent"})
	require.NoError(t, err)

	objs := []string{}
	for e := range evts {
		objs = append(objs, e.Object)
	}
	require.Equal(t, []string{"", "after"}, objs) // First event has empty Object set by mockFlow
}

// TestLLMAgent_CallbackContextPropagation tests that context values set in
// BeforeAgent callback can be retrieved in AfterAgent callback.
func TestLLMAgent_CallbackContextPropagation(t *testing.T) {
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value-from-before"

	// Create callbacks that set and read context values.
	callbacks := agent.NewCallbacks()
	var capturedValue any

	// BeforeAgent callback sets a context value.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &agent.BeforeAgentResult{
			Context: ctxWithValue,
		}, nil
	})

	// AfterAgent callback reads the context value.
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Create agent with callbacks.
	agt := New("test-agent", WithAgentCallbacks(callbacks))
	agt.flow = &mockFlow{} // Use mock flow to avoid heavy dependencies

	// Run the agent.
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("test"),
	}

	events, err := agt.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume all events to ensure callbacks are executed.
	for range events {
	}

	// Verify that the context value was captured in AfterAgent callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeAgent to AfterAgent")
}

func TestLLMAgent_Run_StreamOverride(t *testing.T) {
	a := New("agent")
	a.flow = &mockFlow{done: true}

	stream := false
	invocation := &agent.Invocation{
		InvocationID: "id",
		AgentName:    "agent",
		RunOptions: agent.RunOptions{
			Stream: &stream,
		},
	}

	events, err := a.Run(context.Background(), invocation)
	require.NoError(t, err)

	for range events {
	}
}

func TestLLMAgent_Run_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	a := New("agent")
	a.flow = &mockFlow{done: true}
	invocation := &agent.Invocation{
		InvocationID: "id-disable-tracing",
		Message:      model.NewUserMessage("hi"),
		RunOptions: agent.RunOptions{
			DisableTracing: true,
		},
	}
	events, err := agent.RunWithPlugins(context.Background(), invocation, a)
	require.NoError(t, err)
	for range events {
	}
	require.Empty(t, recorder.Ended())
}
