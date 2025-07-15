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

package graph_test

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestGraphBuilder(t *testing.T) {
	// Create a simple linear graph
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start Node").
		AddFunctionNode("process", "Process Data", "Processes input data", func(ctx context.Context, state graph.State) (graph.State, error) {
			input, ok := state["input"].(string)
			if !ok {
				return state, fmt.Errorf("no input found")
			}
			state["processed"] = fmt.Sprintf("Processed: %s", input)
			return state, nil
		}).
		AddEndNode("end", "End Node").
		AddEdge("start", "process").
		AddEdge("process", "end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	if g == nil {
		t.Fatal("Graph is nil")
	}

	// Validate the graph
	if err := g.Validate(); err != nil {
		t.Fatalf("Graph validation failed: %v", err)
	}
}

func TestConditionalGraph(t *testing.T) {
	// Create a graph with conditional routing
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start Node").
		AddConditionNode("decision", "Decision Node", "Routes based on input", func(ctx context.Context, state graph.State) (string, error) {
			input, ok := state["input"].(string)
			if !ok {
				return "error", fmt.Errorf("no input found")
			}
			if len(input) > 10 {
				return "long_process", nil
			}
			return "short_process", nil
		}).
		AddFunctionNode("long_process", "Long Process", "Handles long inputs", func(ctx context.Context, state graph.State) (graph.State, error) {
			state["result"] = "Long processing completed"
			return state, nil
		}).
		AddFunctionNode("short_process", "Short Process", "Handles short inputs", func(ctx context.Context, state graph.State) (graph.State, error) {
			state["result"] = "Short processing completed"
			return state, nil
		}).
		AddEndNode("end", "End Node").
		AddEdge("start", "decision").
		AddEdge("decision", "long_process").
		AddEdge("decision", "short_process").
		AddEdge("long_process", "end").
		AddEdge("short_process", "end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build conditional graph: %v", err)
	}

	// Validate the graph
	if err := g.Validate(); err != nil {
		t.Fatalf("Conditional graph validation failed: %v", err)
	}
}

func ExampleBuilder() {
	// Create a simple processing pipeline
	graph, err := graph.NewBuilder().
		AddStartNode("start", "Start Processing").
		AddFunctionNode("validate", "Validate Input", "Validates the input data", func(ctx context.Context, state graph.State) (graph.State, error) {
			input := state["input"].(string)
			if input == "" {
				return state, fmt.Errorf("empty input")
			}
			state["validated"] = true
			return state, nil
		}).
		AddFunctionNode("transform", "Transform Data", "Transforms the validated data", func(ctx context.Context, state graph.State) (graph.State, error) {
			input := state["input"].(string)
			state["transformed"] = fmt.Sprintf("TRANSFORMED: %s", input)
			return state, nil
		}).
		AddEndNode("end", "End Processing").
		AddEdge("start", "validate").
		AddEdge("validate", "transform").
		AddEdge("transform", "end").
		Build()

	if err != nil {
		fmt.Printf("Error building graph: %v\n", err)
		return
	}

	startNode := graph.GetStartNode()
	if startNode != "" {
		fmt.Println("Graph created successfully")
	}
	// Output: Graph created successfully
}