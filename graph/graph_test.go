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

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)
	assert.NotNil(t, g, "Expected non-nil graph")
	assert.NotNil(t, g.nodes, "Expected nodes map to be initialized")
	assert.NotNil(t, g.edges, "Expected edges map to be initialized")
	assert.NotNil(t, g.schema, "Expected schema to be set")
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
	assert.NoError(t, err, "Expected no error")

	// Verify node was added.
	retrievedNode, exists := g.Node("test-node")
	assert.True(t, exists, "Expected node to exist")
	assert.Equal(t, "Test Node", retrievedNode.Name, "Expected name 'Test Node'")
	assert.NotNil(t, retrievedNode.Function, "Expected node to have function")
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
	assert.NoError(t, err, "Expected no error")

	// Verify edge was added.
	edges := g.Edges("node1")
	assert.Equal(t, 1, len(edges), "Expected 1 edge")
	assert.Equal(t, "node2", edges[0].To, "Expected edge to 'node2'")
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

	assert.Equal(t, 2, result["counter"], "Expected counter 2")

	items, ok := result["items"].([]any)
	assert.True(t, ok, "Expected items to be a slice")
	assert.Equal(t, 1, len(items), "Expected 1 item")
	assert.Equal(t, "item1", items[0], "Expected items [item1]")
}

func TestStateReducers(t *testing.T) {
	// Test DefaultReducer.
	result := DefaultReducer("old", "new")
	assert.Equal(t, "new", result, "Expected 'new'")

	// Test AppendReducer.
	existing := []any{"a", "b"}
	update := []any{"c", "d"}
	result = AppendReducer(existing, update)
	resultSlice := result.([]any)
	assert.Equal(t, 4, len(resultSlice), "Expected length 4")

	// Test MergeReducer.
	existingMap := map[string]any{"a": 1, "b": 2}
	updateMap := map[string]any{"b": 3, "c": 4}
	result = MergeReducer(existingMap, updateMap)
	resultMap := result.(map[string]any)
	assert.Equal(t, 1, resultMap["a"], "Expected '1'")
	assert.Equal(t, 3, resultMap["b"], "Expected '3'")
	assert.Equal(t, 4, resultMap["c"], "Expected '4'")
}
