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

package graph

import (
	"context"
	"reflect"
	"testing"
)

func TestNewBuilder(t *testing.T) {
	builder := NewBuilder()
	if builder == nil {
		t.Fatal("Expected non-nil builder")
	}
	if builder.stateGraph == nil {
		t.Error("Expected builder to have initialized state graph")
	}
}

func TestBuilderAddFunctionNode(t *testing.T) {
	builder := NewBuilder()

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	result := builder.AddFunctionNode("test", "Test Node", "Test description", testFunc)
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	graph, err := builder.
		SetEntryPoint("test").
		SetFinishPoint("test").
		Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	node, exists := graph.GetNode("test")
	if !exists {
		t.Error("Expected test node to be added")
	}
	if node.Name != "Test Node" {
		t.Errorf("Expected node name 'Test Node', got '%s'", node.Name)
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestBuilderEdges(t *testing.T) {
	builder := NewBuilder()

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	graph, err := builder.
		AddFunctionNode("node1", "Node 1", "First node", testFunc).
		AddFunctionNode("node2", "Node 2", "Second node", testFunc).
		SetEntryPoint("node1").
		AddEdge("node1", "node2").
		SetFinishPoint("node2").
		Build()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	if graph.GetEntryPoint() != "node1" {
		t.Errorf("Expected entry point 'node1', got '%s'", graph.GetEntryPoint())
	}

	edges := graph.GetEdges("node1")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge from node1, got %d", len(edges))
	}
	if edges[0].To != "node2" {
		t.Errorf("Expected edge to node2, got %s", edges[0].To)
	}
}

func TestStateGraphBasic(t *testing.T) {
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("output", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	sg := NewStateGraph(schema)
	if sg == nil {
		t.Fatal("Expected non-nil StateGraph")
	}

	testFunc := func(ctx context.Context, state State) (any, error) {
		input := state["input"].(string)
		return State{"output": "processed: " + input}, nil
	}

	graph, err := sg.
		AddNode("process", testFunc).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	node, exists := graph.GetNode("process")
	if !exists {
		t.Error("Expected process node to exist")
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestConditionalEdges(t *testing.T) {
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	routingFunc := func(ctx context.Context, state State) (string, error) {
		input := state["input"].(string)
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
	condEdge, exists := graph.GetConditionalEdge("router")
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
