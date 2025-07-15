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
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// mockAgentResolver implements AgentResolver for testing.
type mockAgentResolver struct {
	agents map[string]AgentExecutor
}

func (m *mockAgentResolver) ResolveAgent(name string) (AgentExecutor, error) {
	if agent, exists := m.agents[name]; exists {
		return agent, nil
	}
	return nil, errors.New("agent not found")
}

// mockAgentExecutor implements AgentExecutor for testing.
type mockAgentExecutor struct {
	name     string
	response string
	err      error
}

func (m *mockAgentExecutor) Execute(ctx context.Context,
	state State) (State, <-chan *event.Event, error) {
	if m.err != nil {
		return nil, nil, m.err
	}

	newState := state.Clone()
	newState["agent_output"] = m.response

	eventChan := make(chan *event.Event, 1)
	close(eventChan)

	return newState, eventChan, nil
}

func TestNewExecutor(t *testing.T) {
	// Test with valid graph.
	g := createValidGraph()
	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if executor == nil {
		t.Fatal("Expected non-nil executor")
	}

	// Test with invalid graph.
	invalidGraph := New()
	_, err = NewExecutor(invalidGraph, resolver)
	if err == nil {
		t.Error("Expected error for invalid graph")
	}
}

func TestExecuteSimpleGraph(t *testing.T) {
	g := createValidGraph()
	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"input": "test"}
	events, err := executor.Execute(context.Background(), initialState,
		"test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	var eventCount int
	var completionFound bool

	for event := range events {
		eventCount++
		if event.Response != nil && event.Response.Done {
			completionFound = true
		}
	}

	if !completionFound {
		t.Error("Expected completion event")
	}
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestExecuteWithFunctionNode(t *testing.T) {
	g := New()

	// Add nodes.
	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	funcNode := &Node{
		ID:   "func",
		Type: NodeTypeFunction,
		Name: "Function",
		Function: func(ctx context.Context, state State) (State, error) {
			state["processed"] = true
			return state, nil
		},
	}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}

	g.AddNode(startNode)
	g.AddNode(funcNode)
	g.AddNode(endNode)

	// Add edges.
	g.AddEdge(&Edge{From: "start", To: "func"})
	g.AddEdge(&Edge{From: "func", To: "end"})

	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}
	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"input": "test"}
	events, err := executor.Execute(context.Background(), initialState,
		"test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	var completionFound bool
	for event := range events {
		if event.Response != nil && event.Response.Done {
			completionFound = true
		}
	}

	if !completionFound {
		t.Error("Expected completion event")
	}
}

func TestExecuteWithAgentNode(t *testing.T) {
	g := New()

	// Add nodes.
	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	agentNode := &Node{
		ID:        "agent",
		Type:      NodeTypeAgent,
		Name:      "Agent",
		AgentName: "test-agent",
	}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}

	g.AddNode(startNode)
	g.AddNode(agentNode)
	g.AddNode(endNode)

	// Add edges.
	g.AddEdge(&Edge{From: "start", To: "agent"})
	g.AddEdge(&Edge{From: "agent", To: "end"})

	// Create resolver with mock agent.
	mockAgent := &mockAgentExecutor{
		name:     "test-agent",
		response: "agent response",
	}
	resolver := &mockAgentResolver{
		agents: map[string]AgentExecutor{"test-agent": mockAgent},
	}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"input": "test"}
	events, err := executor.Execute(context.Background(), initialState,
		"test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	var completionFound bool
	for event := range events {
		if event.Response != nil && event.Response.Done {
			completionFound = true
		}
	}

	if !completionFound {
		t.Error("Expected completion event")
	}
}

func TestExecuteWithConditionNode(t *testing.T) {
	g := New()

	// Add nodes.
	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	conditionNode := &Node{
		ID:   "condition",
		Type: NodeTypeCondition,
		Name: "Condition",
		Condition: func(ctx context.Context, state State) (string, error) {
			if state["route"].(string) == "left" {
				return "left", nil
			}
			return "right", nil
		},
	}
	leftNode := &Node{ID: "left", Type: NodeTypeFunction, Name: "Left"}
	rightNode := &Node{ID: "right", Type: NodeTypeFunction, Name: "Right"}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}

	g.AddNode(startNode)
	g.AddNode(conditionNode)
	g.AddNode(leftNode)
	g.AddNode(rightNode)
	g.AddNode(endNode)

	// Add edges.
	g.AddEdge(&Edge{From: "start", To: "condition"})
	g.AddEdge(&Edge{From: "condition", To: "left"})
	g.AddEdge(&Edge{From: "condition", To: "right"})
	g.AddEdge(&Edge{From: "left", To: "end"})
	g.AddEdge(&Edge{From: "right", To: "end"})

	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}
	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Test left path.
	initialState := State{"route": "left"}
	events, err := executor.Execute(context.Background(), initialState,
		"test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	var completionFound bool
	for event := range events {
		if event.Response != nil && event.Response.Done {
			completionFound = true
		}
	}

	if !completionFound {
		t.Error("Expected completion event")
	}
}

func TestExecuteWithContextCancellation(t *testing.T) {
	g := createValidGraph()
	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	initialState := State{"input": "test"}
	events, err := executor.Execute(ctx, initialState, "test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Process events - should complete or timeout.
	for event := range events {
		// Just consume events, test passes if no panic occurs.
		_ = event
	}

	// Test passes if we reach here without hanging.
}

func TestExecuteWithTimeout(t *testing.T) {
	g := createValidGraph()
	resolver := &mockAgentResolver{agents: make(map[string]AgentExecutor)}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Add a small delay to ensure timeout.
	time.Sleep(2 * time.Millisecond)

	initialState := State{"input": "test"}
	events, err := executor.Execute(ctx, initialState, "test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should complete quickly or timeout.
	select {
	case <-events:
		// Events received.
	case <-time.After(100 * time.Millisecond):
		t.Error("Execution took too long")
	}
}

func TestExecuteWithAgentError(t *testing.T) {
	g := New()

	// Add nodes.
	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	agentNode := &Node{
		ID:        "agent",
		Type:      NodeTypeAgent,
		Name:      "Agent",
		AgentName: "error-agent",
	}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}

	g.AddNode(startNode)
	g.AddNode(agentNode)
	g.AddNode(endNode)

	// Add edges.
	g.AddEdge(&Edge{From: "start", To: "agent"})
	g.AddEdge(&Edge{From: "agent", To: "end"})

	// Create resolver with error agent.
	errorAgent := &mockAgentExecutor{
		name: "error-agent",
		err:  errors.New("agent execution failed"),
	}
	resolver := &mockAgentResolver{
		agents: map[string]AgentExecutor{"error-agent": errorAgent},
	}

	executor, err := NewExecutor(g, resolver)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"input": "test"}
	events, err := executor.Execute(context.Background(), initialState,
		"test-invocation")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should receive error event.
	var errorFound bool
	for event := range events {
		if event.Response != nil && event.Response.Error != nil {
			errorFound = true
		}
	}

	if !errorFound {
		t.Error("Expected error event due to agent failure")
	}
}

// createValidGraph creates a simple valid graph for testing.
func createValidGraph() *Graph {
	g := New()

	startNode := &Node{ID: "start", Type: NodeTypeStart, Name: "Start"}
	endNode := &Node{ID: "end", Type: NodeTypeEnd, Name: "End"}

	g.AddNode(startNode)
	g.AddNode(endNode)
	g.AddEdge(&Edge{From: "start", To: "end"})

	return g
}
