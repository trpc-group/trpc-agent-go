//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"reflect"
	"testing"
)

func TestNew(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)
	if g == nil {
		t.Fatal("Expected non-nil graph")
	}
	if g.nodes == nil {
		t.Error("Expected nodes map to be initialized")
	}
	if g.edges == nil {
		t.Error("Expected edges map to be initialized")
	}
	if g.schema == nil {
		t.Error("Expected schema to be set")
	}
}

func TestAddNode(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)

	// Test adding valid node.
	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	node := &Node{
		ID:       "test-node",
		Name:     "Test Node",
		Function: testFunc,
	}

	err := g.addNode(node)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify node was added.
	retrievedNode, exists := g.Node("test-node")
	if !exists {
		t.Error("Expected node to exist")
	}
	if retrievedNode.Name != "Test Node" {
		t.Errorf("Expected name 'Test Node', got '%s'", retrievedNode.Name)
	}
}

func TestAddEdge(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)

	// Add nodes first.
	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	node1 := &Node{ID: "node1", Name: "Node 1", Function: testFunc}
	node2 := &Node{ID: "node2", Name: "Node 2", Function: testFunc}

	g.addNode(node1)
	g.addNode(node2)

	// Test adding valid edge.
	edge := &Edge{From: "node1", To: "node2"}
	err := g.addEdge(edge)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify edge was added.
	edges := g.Edges("node1")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(edges))
	}
	if edges[0].To != "node2" {
		t.Errorf("Expected edge to 'node2', got '%s'", edges[0].To)
	}
}

func TestValidate_NoStaticReachabilityRequired(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)

	// Two nodes with no edge between them; entry set to nodeA only.
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("A")
	// No edges A->B; should still compile since we don't enforce reachability.

	g, err := sg.Compile()
	if err != nil || g == nil {
		t.Fatalf("Expected compile success without reachability enforcement, got err=%v", err)
	}
}

func TestValidate_DestinationsExistence(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)

	// Add nodes
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) { return s, nil }, WithDestinations(map[string]string{"finish": ""}))
	sg.AddNode("finish", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("start").SetFinishPoint("finish")

	// Should compile: destination 'finish' exists.
	if _, err := sg.Compile(); err != nil {
		t.Fatalf("Expected compile success with valid destinations, got err=%v", err)
	}

	// Now create an invalid destination declaration.
	sg2 := NewStateGraph(schema)
	sg2.AddNode("start", func(ctx context.Context, s State) (any, error) { return s, nil }, WithDestinations(map[string]string{"missing": ""}))
	sg2.SetEntryPoint("start")
	if _, err := sg2.Compile(); err == nil {
		t.Fatal("Expected compile error due to missing declared destination, got nil")
	}
}

func TestStateSchema(t *testing.T) {
	schema := NewStateSchema().
		AddField("counter", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		}).
		AddField("items", StateField{
			Type:    reflect.TypeOf([]any{}),
			Reducer: AppendReducer,
			Default: func() any { return []any{} },
		})

	// Test applying updates.
	state := State{"counter": 1}
	update := State{"counter": 2, "items": []any{"item1"}}

	result := schema.ApplyUpdate(state, update)

	if result["counter"] != 2 {
		t.Errorf("Expected counter 2, got %v", result["counter"])
	}

	items := result["items"].([]any)
	if len(items) != 1 || items[0] != "item1" {
		t.Errorf("Expected items [item1], got %v", items)
	}
}

func TestStateReducers(t *testing.T) {
	// Test DefaultReducer.
	result := DefaultReducer("old", "new")
	if result != "new" {
		t.Errorf("Expected 'new', got %v", result)
	}

	// Test AppendReducer.
	existing := []any{"a", "b"}
	update := []any{"c", "d"}
	result = AppendReducer(existing, update)
	resultSlice := result.([]any)
	if len(resultSlice) != 4 {
		t.Errorf("Expected length 4, got %d", len(resultSlice))
	}

	// Test MergeReducer.
	existingMap := map[string]any{"a": 1, "b": 2}
	updateMap := map[string]any{"b": 3, "c": 4}
	result = MergeReducer(existingMap, updateMap)
	resultMap := result.(map[string]any)
	if resultMap["a"] != 1 || resultMap["b"] != 3 || resultMap["c"] != 4 {
		t.Errorf("Expected merged map, got %v", resultMap)
	}
}
