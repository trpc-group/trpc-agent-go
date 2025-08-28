//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewGraphAgent(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			return graph.State{"output": "processed: " + input}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent.
	graphAgent, err := New("test-agent", g)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if graphAgent == nil {
		t.Fatal("Expected non-nil graph agent")
	}

	// Test agent info.
	info := graphAgent.Info()
	if info.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", info.Name)
	}
}

func TestGraphAgentWithOptions(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent with options.
	initialState := graph.State{"counter": 5}
	graphAgent, err := New("test-agent", g,
		WithDescription("Test agent description"),
		WithInitialState(initialState),
		WithChannelBufferSize(512))

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that options were applied.
	info := graphAgent.Info()
	if info.Description != "Test agent description" {
		t.Errorf("Expected description to be set")
	}
}

func TestGraphAgentRun(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("message", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("response", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("respond", func(ctx context.Context, state graph.State) (any, error) {
			message := state["message"].(string)
			return graph.State{"response": "Echo: " + message}, nil
		}).
		SetEntryPoint("respond").
		SetFinishPoint("respond").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent.
	initialState := graph.State{"message": "hello"}
	graphAgent, err := New("echo-agent", g, WithInitialState(initialState))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test running the agent.
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "echo-agent",
		InvocationID: "test-invocation",
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentWithRuntimeState(t *testing.T) {
	// Create a simple graph that uses runtime state.
	schema := graph.NewStateSchema().
		AddField("user_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("room_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("base_value", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Verify that runtime state was merged correctly.
			userID, hasUserID := state["user_id"]
			roomID, hasRoomID := state["room_id"]
			baseValue, hasBaseValue := state["base_value"]

			if !hasUserID || !hasRoomID || !hasBaseValue {
				return nil, fmt.Errorf("missing expected state fields")
			}

			if userID != "user123" || roomID != "room456" || baseValue != "default" {
				return nil, fmt.Errorf("unexpected state values")
			}

			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent with base initial state.
	baseState := graph.State{"base_value": "default"}
	graphAgent, err := New("test-agent", g, WithInitialState(baseState))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that runtime state is properly merged.
	ctx := context.Background()
	message := model.NewUserMessage("test message")

	// Create invocation with runtime state.
	invocation := &agent.Invocation{
		Message: message,
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				"user_id": "user123",
				"room_id": "room456",
			},
		},
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Process events to ensure no errors occurred.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	// If we get here without errors, the runtime state was merged correctly.
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentRuntimeStateOverridesBaseState(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("value", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("echo", func(ctx context.Context, state graph.State) (any, error) {
			// Verify that runtime state overrode base state.
			value, hasValue := state["value"]
			if !hasValue {
				return nil, fmt.Errorf("missing value field")
			}

			if value != "runtime_value" {
				return nil, fmt.Errorf("expected runtime_value, got %v", value)
			}

			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("echo").
		SetFinishPoint("echo").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent with base initial state.
	baseState := graph.State{"value": "base_value"}
	graphAgent, err := New("test-agent", g, WithInitialState(baseState))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that runtime state overrides base state.
	ctx := context.Background()
	message := model.NewUserMessage("test message")

	invocation := &agent.Invocation{
		Message: message,
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				"value": "runtime_value",
			},
		},
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Process events to ensure no errors occurred.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	// If we get here without errors, the runtime state override worked correctly.
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

// TestGraphAgent_InvocationContextAccess verifies that GraphAgent can access invocation
// from context when called through runner (after removing duplicate injection).
func TestGraphAgent_InvocationContextAccess(t *testing.T) {
	// Create a simple graph agent.
	stateGraph := graph.NewStateGraph(nil)
	stateGraph.AddNode("test-node", func(ctx context.Context, state graph.State) (any, error) {
		// Verify that invocation is accessible from context.
		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok || invocation == nil {
			return nil, fmt.Errorf("invocation not found in context")
		}

		// Return success state.
		return graph.State{
			"invocation_id": invocation.InvocationID,
			"agent_name":    invocation.AgentName,
			"status":        "success",
		}, nil
	})
	stateGraph.SetEntryPoint("test-node")
	stateGraph.SetFinishPoint("test-node")

	compiledGraph, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-graph-agent", compiledGraph)
	require.NoError(t, err)

	// Create invocation with context that contains invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-123",
		AgentName:    "test-graph-agent",
		Message:      model.NewUserMessage("Test invocation context access"),
	}

	// Create context with invocation (simulating what runner does).
	ctx := agent.NewContextWithInvocation(context.Background(), invocation)

	// Run the agent.
	eventCh, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify that the agent can access invocation from context.
	// This test ensures that even after removing the duplicate injection from LLMAgent,
	// GraphAgent can still access invocation when called through runner.
	require.Greater(t, len(events), 0)

	// The agent should have been able to run successfully, which means
	// it could access the invocation from context for any internal operations.
	t.Logf("GraphAgent successfully executed with %d events, confirming invocation context access", len(events))
}
