//
// Tencent is pleased to support the open source community by making tRPC available.
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
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	g := New()
	if g == nil {
		t.Fatal("Expected non-nil graph")
	}
	if g.nodes == nil {
		t.Error("Expected nodes map to be initialized")
	}
	if g.edges == nil {
		t.Error("Expected edges map to be initialized")
	}
	if g.endNodes == nil {
		t.Error("Expected endNodes map to be initialized")
	}
}

func TestAddNode(t *testing.T) {
	g := New()
	
	// Test adding valid node.
	node := &Node{
		ID:   "test-node",
		Type: NodeTypeFunction,
		Name: "Test Node",
	}
	
	err := g.AddNode(node)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	// Test adding node with empty ID.
	emptyIDNode := &Node{
		Type: NodeTypeFunction,
		Name: "Empty ID Node",
	}
	
	err = g.AddNode(emptyIDNode)
	if err == nil {
		t.Error("Expected error for empty node ID")
	}
	
	// Test adding duplicate node.
	duplicateNode := &Node{
		ID:   "test-node",
		Type: NodeTypeFunction,
		Name: "Duplicate Node",
	}
	
	err = g.AddNode(duplicateNode)
	if err == nil {
		t.Error("Expected error for duplicate node ID")
	}
}

func TestAddStartNode(t *testing.T) {
	g := New()
	
	// Test adding first start node.
	startNode := &Node{
		ID:   "start",
		Type: NodeTypeStart,
		Name: "Start Node",
	}
	
	err := g.AddNode(startNode)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	if g.startNode != "start" {
		t.Errorf("Expected start node to be 'start', got '%s'", g.startNode)
	}
	
	// Test adding second start node.
	secondStartNode := &Node{
		ID:   "start2",
		Type: NodeTypeStart,
		Name: "Second Start Node",
	}
	
	err = g.AddNode(secondStartNode)
	if err == nil {
		t.Error("Expected error for second start node")
	}
}

func TestAddEndNode(t *testing.T) {
	g := New()
	
	endNode := &Node{
		ID:   "end",
		Type: NodeTypeEnd,
		Name: "End Node",
	}
	
	err := g.AddNode(endNode)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	if !g.endNodes["end"] {
		t.Error("Expected end node to be registered")
	}
}

func TestAddEdge(t *testing.T) {
	g := New()
	
	// Add nodes first.
	node1 := &Node{ID: "node1", Type: NodeTypeFunction, Name: "Node 1"}
	node2 := &Node{ID: "node2", Type: NodeTypeFunction, Name: "Node 2"}
	
	g.AddNode(node1)
	g.AddNode(node2)
	
	// Test adding valid edge.
	edge := &Edge{From: "node1", To: "node2"}
	err := g.AddEdge(edge)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	// Test adding edge with empty from.
	emptyFromEdge := &Edge{From: "", To: "node2"}
	err = g.AddEdge(emptyFromEdge)
	if err == nil {
		t.Error("Expected error for empty from field")
	}
	
	// Test adding edge with empty to.
	emptyToEdge := &Edge{From: "node1", To: ""}
	err = g.AddEdge(emptyToEdge)
	if err == nil {
		t.Error("Expected error for empty to field")
	}
	
	// Test adding edge with non-existent source node.
	nonExistentFromEdge := &Edge{From: "nonexistent", To: "node2"}
	err = g.AddEdge(nonExistentFromEdge)
	if err == nil {
		t.Error("Expected error for non-existent source node")
	}
	
	// Test adding edge with non-existent target node.
	nonExistentToEdge := &Edge{From: "node1", To: "nonexistent"}
	err = g.AddEdge(nonExistentToEdge)
	if err == nil {
		t.Error("Expected error for non-existent target node")
	}
}

func TestGetNode(t *testing.T) {
	g := New()
	
	node := &Node{ID: "test", Type: NodeTypeFunction, Name: "Test"}
	g.AddNode(node)
	
	// Test getting existing node.
	retrieved, exists := g.GetNode("test")
	if !exists {
		t.Error("Expected node to exist")
	}
	if retrieved.ID != "test" {
		t.Errorf("Expected node ID 'test', got '%s'", retrieved.ID)
	}
	
	// Test getting non-existent node.
	_, exists = g.GetNode("nonexistent")
	if exists {
		t.Error("Expected node not to exist")
	}
}

func TestGetEdges(t *testing.T) {
	g := New()
	
	node1 := &Node{ID: "node1", Type: NodeTypeFunction, Name: "Node 1"}
	node2 := &Node{ID: "node2", Type: NodeTypeFunction, Name: "Node 2"}
	
	g.AddNode(node1)
	g.AddNode(node2)
	
	edge := &Edge{From: "node1", To: "node2"}
	g.AddEdge(edge)
	
	edges := g.GetEdges("node1")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(edges))
	}
	if edges[0].To != "node2" {
		t.Errorf("Expected edge to 'node2', got '%s'", edges[0].To)
	}
	
	// Test getting edges for node with no outgoing edges.
	edges = g.GetEdges("node2")
	if len(edges) != 0 {
		t.Errorf("Expected 0 edges, got %d", len(edges))
	}
}

func TestValidate(t *testing.T) {
	// Test valid graph.
	g := New()
	
	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}
	
	g.AddNode(startNode)
	g.AddNode(endNode)
	g.AddEdge(&Edge{From: "start", To: "end"})
	
	err := g.Validate()
	if err != nil {
		t.Errorf("Expected valid graph, got error: %v", err)
	}
	
	// Test graph without start node.
	g2 := New()
	endNode2 := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}
	g2.AddNode(endNode2)
	
	err = g2.Validate()
	if err == nil {
		t.Error("Expected error for graph without start node")
	}
	
	// Test graph without end node.
	g3 := New()
	startNode3 := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	g3.AddNode(startNode3)
	
	err = g3.Validate()
	if err == nil {
		t.Error("Expected error for graph without end node")
	}
}

func TestStateClone(t *testing.T) {
	original := State{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}
	
	cloned := original.Clone()
	
	// Test that clone has same values.
	if cloned["key1"] != "value1" {
		t.Error("Clone should have same string value")
	}
	if cloned["key2"] != 42 {
		t.Error("Clone should have same int value")
	}
	if cloned["key3"] != true {
		t.Error("Clone should have same bool value")
	}
	
	// Test that modifying clone doesn't affect original.
	cloned["key1"] = "modified"
	if original["key1"] == "modified" {
		t.Error("Modifying clone should not affect original")
	}
}

func TestNodeFunctions(t *testing.T) {
	testFunc := func(ctx context.Context, state State) (State, error) {
		state["executed"] = true
		return state, nil
	}
	
	node := &Node{
		ID:       "func-node",
		Type:     NodeTypeFunction,
		Name:     "Function Node",
		Function: testFunc,
	}
	
	if node.Function == nil {
		t.Error("Expected function to be set")
	}
	
	// Test function execution.
	state := make(State)
	newState, err := node.Function(context.Background(), state)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !newState["executed"].(bool) {
		t.Error("Expected function to modify state")
	}
}

func TestConditionFunctions(t *testing.T) {
	conditionFunc := func(ctx context.Context, state State) (string, error) {
		if state["condition"].(bool) {
			return "true_path", nil
		}
		return "false_path", nil
	}
	
	node := &Node{
		ID:        "condition-node",
		Type:      NodeTypeCondition,
		Name:      "Condition Node",
		Condition: conditionFunc,
	}
	
	if node.Condition == nil {
		t.Error("Expected condition function to be set")
	}
	
	// Test condition execution with true.
	state := State{"condition": true}
	result, err := node.Condition(context.Background(), state)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "true_path" {
		t.Errorf("Expected 'true_path', got '%s'", result)
	}
	
	// Test condition execution with false.
	state = State{"condition": false}
	result, err = node.Condition(context.Background(), state)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "false_path" {
		t.Errorf("Expected 'false_path', got '%s'", result)
	}
}

func TestConditionError(t *testing.T) {
	conditionFunc := func(ctx context.Context, state State) (string, error) {
		return "", errors.New("condition error")
	}
	
	node := &Node{
		ID:        "error-condition",
		Type:      NodeTypeCondition,
		Name:      "Error Condition",
		Condition: conditionFunc,
	}
	
	state := make(State)
	_, err := node.Condition(context.Background(), state)
	if err == nil {
		t.Error("Expected error from condition function")
	}
}