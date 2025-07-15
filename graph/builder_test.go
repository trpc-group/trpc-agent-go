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
	"testing"
)

func TestNewBuilder(t *testing.T) {
	builder := NewBuilder()
	if builder == nil {
		t.Fatal("Expected non-nil builder")
	}
	if builder.graph == nil {
		t.Error("Expected builder to have initialized graph")
	}
}

func TestBuilderAddStartNode(t *testing.T) {
	builder := NewBuilder()

	result := builder.AddStartNode("start", "Start Node")
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	node, exists := builder.graph.GetNode("start")
	if !exists {
		t.Error("Expected start node to be added")
	}
	if node.Type != NodeTypeStart {
		t.Errorf("Expected node type %s, got %s", NodeTypeStart, node.Type)
	}
	if node.Name != "Start Node" {
		t.Errorf("Expected node name 'Start Node', got '%s'", node.Name)
	}
}

func TestBuilderAddEndNode(t *testing.T) {
	builder := NewBuilder()

	result := builder.AddEndNode("end", "End Node")
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	node, exists := builder.graph.GetNode("end")
	if !exists {
		t.Error("Expected end node to be added")
	}
	if node.Type != NodeTypeEnd {
		t.Errorf("Expected node type %s, got %s", NodeTypeEnd, node.Type)
	}
}

func TestBuilderAddFunctionNode(t *testing.T) {
	builder := NewBuilder()

	testFunc := func(ctx context.Context, state State) (State, error) {
		return state, nil
	}

	result := builder.AddFunctionNode("func", "Function Node",
		"Test function", testFunc)
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	node, exists := builder.graph.GetNode("func")
	if !exists {
		t.Error("Expected function node to be added")
	}
	if node.Type != NodeTypeFunction {
		t.Errorf("Expected node type %s, got %s", NodeTypeFunction, node.Type)
	}
	if node.Function == nil {
		t.Error("Expected function to be set")
	}
	if node.Description != "Test function" {
		t.Errorf("Expected description 'Test function', got '%s'",
			node.Description)
	}
}

func TestBuilderAddAgentNode(t *testing.T) {
	builder := NewBuilder()

	result := builder.AddAgentNode("agent", "Agent Node",
		"Test agent", "test-agent")
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	node, exists := builder.graph.GetNode("agent")
	if !exists {
		t.Error("Expected agent node to be added")
	}
	if node.Type != NodeTypeAgent {
		t.Errorf("Expected node type %s, got %s", NodeTypeAgent, node.Type)
	}
	if node.AgentName != "test-agent" {
		t.Errorf("Expected agent name 'test-agent', got '%s'",
			node.AgentName)
	}
}

func TestBuilderAddConditionNode(t *testing.T) {
	builder := NewBuilder()

	conditionFunc := func(ctx context.Context, state State) (string, error) {
		return "next", nil
	}

	result := builder.AddConditionNode("condition", "Condition Node",
		"Test condition", conditionFunc)
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	node, exists := builder.graph.GetNode("condition")
	if !exists {
		t.Error("Expected condition node to be added")
	}
	if node.Type != NodeTypeCondition {
		t.Errorf("Expected node type %s, got %s", NodeTypeCondition,
			node.Type)
	}
	if node.Condition == nil {
		t.Error("Expected condition function to be set")
	}
}

func TestBuilderAddEdge(t *testing.T) {
	builder := NewBuilder()

	// Add nodes first.
	builder.AddStartNode("start", "Start")
	builder.AddEndNode("end", "End")

	result := builder.AddEdge("start", "end")
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	edges := builder.graph.GetEdges("start")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(edges))
	}
	if edges[0].To != "end" {
		t.Errorf("Expected edge to 'end', got '%s'", edges[0].To)
	}
}

func TestBuilderAddConditionalEdge(t *testing.T) {
	builder := NewBuilder()

	// Add nodes first.
	builder.AddStartNode("start", "Start")
	builder.AddEndNode("end", "End")

	result := builder.AddConditionalEdge("start", "end", "condition")
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	edges := builder.graph.GetEdges("start")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(edges))
	}
	if edges[0].Condition != "condition" {
		t.Errorf("Expected edge condition 'condition', got '%s'",
			edges[0].Condition)
	}
}

func TestBuilderBuild(t *testing.T) {
	builder := NewBuilder()

	// Create valid graph.
	builder.AddStartNode("start", "Start")
	builder.AddEndNode("end", "End")
	builder.AddEdge("start", "end")

	graph, err := builder.Build()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if graph == nil {
		t.Fatal("Expected non-nil graph")
	}

	// Test validation is called.
	err = graph.Validate()
	if err != nil {
		t.Errorf("Expected valid graph, got error: %v", err)
	}
}

func TestBuilderBuildInvalid(t *testing.T) {
	builder := NewBuilder()

	// Create invalid graph (no start node).
	builder.AddEndNode("end", "End")

	_, err := builder.Build()
	if err == nil {
		t.Error("Expected error for invalid graph")
	}
}

func TestBuilderMustBuild(t *testing.T) {
	builder := NewBuilder()

	// Create valid graph.
	builder.AddStartNode("start", "Start")
	builder.AddEndNode("end", "End")
	builder.AddEdge("start", "end")

	// Should not panic.
	graph := builder.MustBuild()
	if graph == nil {
		t.Fatal("Expected non-nil graph")
	}
}

func TestBuilderMustBuildPanic(t *testing.T) {
	builder := NewBuilder()

	// Create invalid graph.
	builder.AddEndNode("end", "End")

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for invalid graph")
		}
	}()

	builder.MustBuild()
}

func TestBuilderChaining(t *testing.T) {
	// Test that all methods can be chained.
	graph, err := NewBuilder().
		AddStartNode("start", "Start").
		AddFunctionNode("func", "Function", "Test",
			func(ctx context.Context, state State) (State, error) {
				return state, nil
			}).
		AddAgentNode("agent", "Agent", "Test", "test-agent").
		AddConditionNode("condition", "Condition", "Test",
			func(ctx context.Context, state State) (string, error) {
				return "next", nil
			}).
		AddEndNode("end", "End").
		AddEdge("start", "func").
		AddEdge("func", "agent").
		AddEdge("agent", "condition").
		AddEdge("condition", "end").
		AddConditionalEdge("condition", "end", "default").
		Build()

	if err != nil {
		t.Fatalf("Expected no error from chained building, got %v", err)
	}
	if graph == nil {
		t.Fatal("Expected non-nil graph from chained building")
	}
}
