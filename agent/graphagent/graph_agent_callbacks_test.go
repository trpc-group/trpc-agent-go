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
