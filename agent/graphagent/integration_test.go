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
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestBasicIntegration(t *testing.T) {
	// Create a simple workflow graph using the new API
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("prepared", graph.StateField{
			Type:    reflect.TypeOf(false),
			Reducer: graph.DefaultReducer,
		}).
		AddField("processed", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("finalized", graph.StateField{
			Type:    reflect.TypeOf(false),
			Reducer: graph.DefaultReducer,
		})

	workflowGraph, err := graph.NewStateGraph(schema).
		AddNode("prepare", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"prepared": true,
				"input":    "prepared data",
			}, nil
		}).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			return graph.State{"processed": "processed: " + input}, nil
		}).
		AddNode("finalize", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"finalized": true}, nil
		}).
		SetEntryPoint("prepare").
		AddEdge("prepare", "process").
		AddEdge("process", "finalize").
		SetFinishPoint("finalize").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build workflow graph: %v", err)
	}

	// Create GraphAgent with the workflow
	graphAgent, err := New("workflow-agent", workflowGraph,
		WithDescription("Integration test workflow agent"),
		WithInitialState(graph.State{"input": "initial data"}))

	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test running the workflow
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "workflow-agent",
		InvocationID: "integration-test",
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Failed to run graph agent: %v", err)
	}

	// Collect all events
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("Expected at least one event from workflow execution")
	}

	t.Logf("Integration test completed successfully with %d events", eventCount)
}
