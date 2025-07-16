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

func TestNewExecutor(t *testing.T) {
	// Test with valid graph.
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("output", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	g := New(schema)
	g.AddNode(&Node{
		ID:   "process",
		Name: "Process",
		Function: func(ctx context.Context, state State) (any, error) {
			input := state["input"].(string)
			return State{"output": "processed: " + input}, nil
		},
	})
	g.SetEntryPoint("process")
	g.AddEdge(&Edge{From: "process", To: End})

	executor, err := NewExecutor(g)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if executor == nil {
		t.Fatal("Expected non-nil executor")
	}

	// Test with invalid graph (no entry point).
	invalidGraph := New(schema)
	_, err = NewExecutor(invalidGraph)
	if err == nil {
		t.Error("Expected error for invalid graph")
	}
}

func TestExecutorInvoke(t *testing.T) {
	// Create a simple graph.
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("output", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	g := New(schema)
	g.AddNode(&Node{
		ID:   "transform",
		Name: "Transform",
		Function: func(ctx context.Context, state State) (any, error) {
			input := state["input"].(string)
			return State{"output": "transformed: " + input}, nil
		},
	})
	g.SetEntryPoint("transform")
	g.AddEdge(&Edge{From: "transform", To: End})

	executor, err := NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"input": "test data"}
	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if finalState["output"] != "transformed: test data" {
		t.Errorf("Expected 'transformed: test data', got %v", finalState["output"])
	}
}

func TestExecutorStreamExecution(t *testing.T) {
	// Create a simple graph.
	schema := NewStateSchema().
		AddField("counter", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		})

	g := New(schema)
	g.AddNode(&Node{
		ID:   "increment",
		Name: "Increment",
		Function: func(ctx context.Context, state State) (any, error) {
			counter, _ := state["counter"].(int)
			return State{"counter": counter + 1}, nil
		},
	})
	g.SetEntryPoint("increment")
	g.AddEdge(&Edge{From: "increment", To: End})

	executor, err := NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := State{"counter": 0}
	events, err := executor.Execute(context.Background(), initialState, "test-stream")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Collect events to ensure streaming works.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}
