//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewBuilder(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())
	if builder == nil {
		t.Fatal("Expected non-nil builder")
	}
	if builder.graph == nil {
		t.Error("Expected builder to have initialized state graph")
	}
}

func TestBuilderAddFunctionNode(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	result := builder.AddNode("test", testFunc)
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	graph, err := builder.
		SetEntryPoint("test").
		SetFinishPoint("test").
		Compile()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	node, exists := graph.Node("test")
	if !exists {
		t.Error("Expected test node to be added")
	}
	if node.Name != "test" {
		t.Errorf("Expected node name 'test', got '%s'", node.Name)
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestBuilderEdges(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	graph, err := builder.
		AddNode("node1", testFunc).
		AddNode("node2", testFunc).
		SetEntryPoint("node1").
		AddEdge("node1", "node2").
		SetFinishPoint("node2").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	if graph.EntryPoint() != "node1" {
		t.Errorf("Expected entry point 'node1', got '%s'", graph.EntryPoint())
	}

	edges := graph.Edges("node1")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge from node1, got %d", len(edges))
	}
	if edges[0].To != "node2" {
		t.Errorf("Expected edge to node2, got %s", edges[0].To)
	}
}

func TestStateGraphBasic(t *testing.T) {
	schema := NewStateSchema().
		AddField(StateKeyUserInput, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField(StateKeyLastResponse, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	sg := NewStateGraph(schema)
	if sg == nil {
		t.Fatal("Expected non-nil StateGraph")
	}

	testFunc := func(ctx context.Context, state State) (any, error) {
		input := state[StateKeyUserInput].(string)
		return State{StateKeyLastResponse: "processed: " + input}, nil
	}

	graph, err := sg.
		AddNode("process", testFunc).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	node, exists := graph.Node("process")
	if !exists {
		t.Error("Expected process node to exist")
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestConditionalEdges(t *testing.T) {
	schema := NewStateSchema().
		AddField(StateKeyUserInput, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField(StateKeyLastResponse, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	routingFunc := func(ctx context.Context, state State) (string, error) {
		input := state[StateKeyUserInput].(string)
		if len(input) > 5 {
			return "long", nil
		}
		return "short", nil
	}

	passThrough := func(ctx context.Context, state State) (any, error) {
		return State(state), nil
	}

	processLong := func(ctx context.Context, state State) (any, error) {
		return State{"result": "long processing"}, nil
	}

	processShort := func(ctx context.Context, state State) (any, error) {
		return State{"result": "short processing"}, nil
	}

	graph, err := NewStateGraph(schema).
		AddNode("router", passThrough).
		AddNode("long_process", processLong).
		AddNode("short_process", processShort).
		SetEntryPoint("router").
		AddConditionalEdges("router", routingFunc, map[string]string{
			"long":  "long_process",
			"short": "short_process",
		}).
		SetFinishPoint("long_process").
		SetFinishPoint("short_process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Check that conditional edge was added
	condEdge, exists := graph.ConditionalEdge("router")
	if !exists {
		t.Error("Expected conditional edge to exist")
	}
	if condEdge.PathMap["long"] != "long_process" {
		t.Error("Expected correct path mapping for 'long'")
	}
	if condEdge.PathMap["short"] != "short_process" {
		t.Error("Expected correct path mapping for 'short'")
	}
}

func TestConditionalEdgeProcessing(t *testing.T) {
	// Create a simple state schema
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	// Create a conditional function
	conditionFunc := func(ctx context.Context, state State) (string, error) {
		input := state["input"].(string)
		if len(input) > 5 {
			return "long", nil
		}
		return "short", nil
	}

	// Create nodes
	longNode := func(ctx context.Context, state State) (any, error) {
		return State{"result": "processed as long"}, nil
	}

	shortNode := func(ctx context.Context, state State) (any, error) {
		return State{"result": "processed as short"}, nil
	}

	// Build graph
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("start", func(ctx context.Context, state State) (any, error) {
			return state, nil // Pass through
		}).
		AddNode("long", longNode).
		AddNode("short", shortNode).
		SetEntryPoint("start").
		SetFinishPoint("long").
		SetFinishPoint("short").
		AddConditionalEdges("start", conditionFunc, map[string]string{
			"long":  "long",
			"short": "short",
		})

	// Compile graph
	graph, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test with short input
	t.Run("Short Input", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}
		invocation := &agent.Invocation{
			InvocationID: "test-invocation-short",
		}
		eventChan, err := executor.Execute(context.Background(), State{"input": "hi"}, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

		// Process events to completion
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				break
			}
		}

		// Verify that short node was triggered
		// This is a basic test - in a real scenario, you'd check the final state
		t.Log("Short input test completed")
	})

	// Test with long input
	t.Run("Long Input", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}
		invocation := &agent.Invocation{
			InvocationID: "test-invocation-long",
		}
		eventChan, err := executor.Execute(context.Background(), State{"input": "this is a long input"}, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

		// Process events to completion
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				break
			}
		}

		// Verify that long node was triggered
		t.Log("Long input test completed")
	})
}

func TestConditionalEdgeWithTools(t *testing.T) {
	// Create a state schema for messages
	schema := MessagesStateSchema()

	// Create a tools conditional edge test
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("llm", func(ctx context.Context, state State) (any, error) {
			// Simulate LLM response with tool calls
			return State{
				StateKeyMessages: []model.Message{
					model.NewUserMessage("test"),
					model.NewAssistantMessage("test response"),
				},
			}, nil
		}).
		AddNode("tools", func(ctx context.Context, state State) (any, error) {
			return State{"result": "tools executed"}, nil
		}).
		AddNode("fallback", func(ctx context.Context, state State) (any, error) {
			return State{"result": "fallback executed"}, nil
		}).
		SetEntryPoint("llm").
		SetFinishPoint("tools").
		SetFinishPoint("fallback").
		AddToolsConditionalEdges("llm", "tools", "fallback")

	// Compile graph
	graph, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test execution
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-tools",
	}
	eventChan, err := executor.Execute(context.Background(), State{}, invocation)
	if err != nil {
		t.Fatalf("Failed to execute graph: %v", err)
	}

	// Process events to completion
	for event := range eventChan {
		if event.Error != nil {
			t.Errorf("Execution error: %v", event.Error)
		}
		if event.Done {
			break
		}
	}

	t.Log("Tools conditional edge test completed")
}
