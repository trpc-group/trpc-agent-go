//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// buildTrivialGraph builds a single-node state graph that completes immediately.
func buildTrivialGraph(t *testing.T) *graph.Graph {
	t.Helper()
	schema := graph.NewStateSchema().
		AddField("x", graph.StateField{Type: reflect.TypeOf(0), Reducer: graph.DefaultReducer})
	g, err := graph.NewStateGraph(schema).
		AddNode("only", func(ctx context.Context, s graph.State) (any, error) { return graph.State{"x": 1}, nil }).
		SetEntryPoint("only").
		SetFinishPoint("only").
		Compile()
	require.NoError(t, err)
	return g
}

func TestGraphAgent_BeforeCallback_CustomResponse(t *testing.T) {
	g := buildTrivialGraph(t)
	callbacks := agent.NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
			return &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("short-circuit")}}}, nil
		})
	ga, err := New("ga", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	ch, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	// Should receive exactly one response event from before-callback and close.
	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 1)
	require.Equal(t, model.RoleAssistant, events[0].Response.Choices[0].Message.Role)
	require.Equal(t, "short-circuit", events[0].Response.Choices[0].Message.Content)
}

func TestGraphAgent_BeforeCallback_Error(t *testing.T) {
	g := buildTrivialGraph(t)
	callbacks := agent.NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
			return nil, errTest
		})
	ga, err := New("ga", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	ch, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	// Expect an error event on the stream (plus the barrier).
	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Equal(t, len(events), 1)
	require.Equal(t, model.ObjectTypeError, events[0].Object)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Error.Type)
	require.NotNil(t, events[0].Error.Message)
}

func TestGraphAgent_AfterCallback_CustomResponseAppended(t *testing.T) {
	g := buildTrivialGraph(t)
	callbacks := agent.NewCallbacks().
		RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
			return &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("tail")}}}, nil
		})
	ga, err := New("ga", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{Message: model.NewUserMessage("go")}
	ch, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	var last *event.Event
	count := 0
	for e := range ch {
		last, count = e, count+1
	}
	require.Greater(t, count, 1)
	require.NotNil(t, last)
	require.Equal(t, model.RoleAssistant, last.Response.Choices[0].Message.Role)
	require.Equal(t, "tail", last.Response.Choices[0].Message.Content)
}

func TestGraphAgent_AfterCallback_ErrorEmitsErrorEvent(t *testing.T) {
	g := buildTrivialGraph(t)
	callbacks := agent.NewCallbacks().
		RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
			return nil, errTest
		})
	ga, err := New("ga", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{Message: model.NewUserMessage("go")}
	ch, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	// Expect final error event
	var last *event.Event
	for e := range ch {
		last = e
	}
	require.NotNil(t, last)
	require.Equal(t, model.ObjectTypeError, last.Object)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, last.Error.Type)
}

var errTest = errors.New("cb error")

// TestGraphAgent_CallbackContextPropagation tests that context values set in
// BeforeAgent callback can be retrieved in AfterAgent callback.
func TestGraphAgent_CallbackContextPropagation(t *testing.T) {
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

	// Create graph agent with callbacks.
	g := buildTrivialGraph(t)
	graphAgent, err := New("test-graph", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	// Run the agent.
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-graph",
		Message:      model.NewUserMessage("test"),
	}

	events, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume all events to ensure callbacks are executed.
	for range events {
	}

	// Verify that the context value was captured in AfterAgent callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeAgent to AfterAgent")
}
