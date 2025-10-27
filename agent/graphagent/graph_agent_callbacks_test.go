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

	"github.com/stretchr/testify/assert"
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
	require.Error(t, err)
	require.Nil(t, ch)
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

// TestGraphAgent_CallbackMessage_SharedBetweenBeforeAndAfter verifies that
// the callback message created in BeforeAgent is the same instance in AfterAgent.
func TestGraphAgent_CallbackMessage_SharedBetweenBeforeAndAfter(t *testing.T) {
	// Track the message from Before callback.
	var beforeMsg any

	// Create agent callbacks.
	callbacks := agent.NewCallbacks()

	// Register Before callback that stores data in message.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		msg := agent.CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in BeforeAgent")

		// Store the message for comparison.
		beforeMsg = msg

		// Store test values.
		msg.Set("test_key", "test_value")
		msg.Set("invocation_id", inv.InvocationID)

		return nil, nil
	})

	// Register After callback that retrieves data from message.
	callbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		msg := agent.CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in AfterAgent")

		// Check if it's the same message instance.
		assert.Same(t, beforeMsg, msg,
			"callback message in AfterAgent should be the same instance as in BeforeAgent")

		// Retrieve the value stored in Before callback.
		val, ok := msg.Get("test_key")
		require.True(t, ok, "should be able to get the value set in BeforeAgent")
		require.Equal(t, "test_value", val.(string))

		// Verify invocation_id matches.
		invID, ok := msg.Get("invocation_id")
		require.True(t, ok)
		require.Equal(t, inv.InvocationID, invID.(string))

		return nil, nil
	})

	// Create a simple graph.
	g := buildTrivialGraph(t)

	// Create graph agent with callbacks.
	graphAgent, err := New("test-graph", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-graph",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "test",
		},
	}

	ctx := context.Background()
	eventChan, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Drain the event channel.
	for range eventChan {
	}
}
