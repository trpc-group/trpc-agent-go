//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package graphagent

import (
	"context"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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
