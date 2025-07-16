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

package graph_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStateGraphBasic(t *testing.T) {
	// Create a simple linear graph
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("processed", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	stateGraph := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input, ok := state["input"].(string)
			if !ok {
				return state, fmt.Errorf("no input found")
			}
			return graph.State{"processed": fmt.Sprintf("Processed: %s", input)}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test execution
	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := graph.State{"input": "test data"}
	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["processed"] != "Processed: test data" {
		t.Errorf("Expected processed data, got: %v", finalState["processed"])
	}
}

func TestConditionalEdges(t *testing.T) {
	// Create a graph with conditional routing
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("processing_type", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	// Routing condition function
	routeByLength := func(ctx context.Context, state graph.State) (string, error) {
		input, ok := state["input"].(string)
		if !ok {
			return "error", fmt.Errorf("no input found")
		}
		if len(input) > 10 {
			return "long", nil
		}
		return "short", nil
	}

	stateGraph := graph.NewStateGraph(schema).
		AddNode("decision", func(ctx context.Context, state graph.State) (any, error) {
			// Decision node just passes state through
			return graph.State(state), nil
		}).
		AddNode("long_process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"result":          "Long processing completed",
				"processing_type": "long",
			}, nil
		}).
		AddNode("short_process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"result":          "Short processing completed",
				"processing_type": "short",
			}, nil
		}).
		SetEntryPoint("decision").
		AddConditionalEdges("decision", routeByLength, map[string]string{
			"long":  "long_process",
			"short": "short_process",
		}).
		SetFinishPoint("long_process").
		SetFinishPoint("short_process")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Test short input
	shortState := graph.State{"input": "short"}
	finalState, err := executor.Invoke(context.Background(), shortState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["result"] != "Short processing completed" {
		t.Errorf("Expected short processing result, got: %v", finalState["result"])
	}

	// Test long input
	longState := graph.State{"input": "this is a very long input string"}
	finalState, err = executor.Invoke(context.Background(), longState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["result"] != "Long processing completed" {
		t.Errorf("Expected long processing result, got: %v", finalState["result"])
	}
}

func TestMessagesStateSchema(t *testing.T) {
	// Test using the pre-built messages state schema
	schema := graph.MessagesStateSchema()

	// Add additional fields for this test
	schema.AddField("user_input", graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	}).
		AddField("final_response", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	stateGraph := graph.NewStateGraph(schema).
		AddNode("process_input", func(ctx context.Context, state graph.State) (any, error) {
			userInput := state["user_input"].(string)
			// Simulate processing and creating a response
			return graph.State{
				"final_response": fmt.Sprintf("Bot response to: %s", userInput),
			}, nil
		}).
		SetEntryPoint("process_input").
		SetFinishPoint("process_input")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := graph.State{
		"user_input": "Hello, how are you?",
		"messages":   []model.Message{}, // Start with empty messages of correct type
	}

	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["final_response"] != "Bot response to: Hello, how are you?" {
		t.Errorf("Expected bot response, got: %v", finalState["final_response"])
	}
}

func TestBuilderCompatibility(t *testing.T) {
	// Create a helper function that matches the old signature for the builder
	processFunc := func(ctx context.Context, state graph.State) (any, error) {
		input, ok := state["input"].(string)
		if !ok {
			return state, fmt.Errorf("no input found")
		}
		return graph.State{"processed": fmt.Sprintf("Processed: %s", input)}, nil
	}

	// Test that the legacy Builder still works with the new system
	g, err := graph.NewBuilder().
		AddFunctionNode("process", "Process Data", "Processes input data", processFunc).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Build()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := graph.State{"input": "legacy test"}
	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["processed"] != "Processed: legacy test" {
		t.Errorf("Expected processed data, got: %v", finalState["processed"])
	}
}

func TestStateReducers(t *testing.T) {
	// Test different reducer types
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		}).
		AddField("items", graph.StateField{
			Type:    reflect.TypeOf([]any{}),
			Reducer: graph.AppendReducer,
			Default: func() any { return []any{} },
		}).
		AddField("metadata", graph.StateField{
			Type:    reflect.TypeOf(map[string]any{}),
			Reducer: graph.MergeReducer,
			Default: func() any { return make(map[string]any) },
		})

	stateGraph := graph.NewStateGraph(schema).
		AddNode("step1", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"counter":  1,
				"items":    []any{"item1"},
				"metadata": map[string]any{"step": "1"},
			}, nil
		}).
		AddNode("step2", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"counter":  2,                                            // This should override
				"items":    []any{"item2"},                               // This should append
				"metadata": map[string]any{"step": "2", "extra": "data"}, // This should merge
			}, nil
		}).
		SetEntryPoint("step1").
		AddEdge("step1", "step2").
		SetFinishPoint("step2")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := graph.State{}
	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	// Check counter (should be overridden)
	if finalState["counter"] != 2 {
		t.Errorf("Expected counter to be 2, got: %v", finalState["counter"])
	}

	// Check items (should be appended)
	items := finalState["items"].([]any)
	if len(items) != 2 || items[0] != "item1" || items[1] != "item2" {
		t.Errorf("Expected items to be appended, got: %v", items)
	}

	// Check metadata (should be merged)
	metadata := finalState["metadata"].(map[string]any)
	if metadata["step"] != "2" || metadata["extra"] != "data" {
		t.Errorf("Expected metadata to be merged, got: %v", metadata)
	}
}

func TestDocumentProcessingWorkflow(t *testing.T) {
	// Test a realistic document processing workflow using a specialized schema
	// Create a document processing schema locally (no longer part of framework)
	schema := graph.MessagesStateSchema()

	// Add document processing specific fields
	schema.AddField("document", graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	}).
		AddField("processed_content", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("analysis_results", graph.StateField{
			Type:    reflect.TypeOf(map[string]any{}),
			Reducer: graph.MergeReducer,
			Default: func() any { return make(map[string]any) },
		}).
		AddField("quality_score", graph.StateField{
			Type:    reflect.TypeOf(float64(0)),
			Reducer: graph.DefaultReducer,
		}).
		AddField("processing_steps", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})

	stateGraph := graph.NewStateGraph(schema).
		AddNode("preprocess", func(ctx context.Context, state graph.State) (any, error) {
			document := state["document"].(string)
			return graph.State{
				"processed_content": fmt.Sprintf("Preprocessed: %s", document),
				"processing_steps":  []string{"preprocess"}, // Single step wrapped in slice for append
				"analysis_results": map[string]any{
					"word_count": len(document),
				},
			}, nil
		}).
		AddNode("analyze", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"processing_steps": []string{"analyze"}, // Single step wrapped in slice for append
				"analysis_results": map[string]any{
					"complexity": "simple",
					"sentiment":  "neutral",
				},
				"quality_score": 0.8,
			}, nil
		}).
		AddNode("finalize", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"processing_steps": []string{"finalize"}, // Single step wrapped in slice for append
				"analysis_results": map[string]any{
					"status": "completed",
				},
			}, nil
		}).
		SetEntryPoint("preprocess").
		AddEdge("preprocess", "analyze").
		AddEdge("analyze", "finalize").
		SetFinishPoint("finalize")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	initialState := graph.State{
		"document": "This is a sample document for processing.",
	}

	finalState, err := executor.Invoke(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	// Verify processing steps were accumulated
	steps := finalState["processing_steps"].([]string)
	expected := []string{"preprocess", "analyze", "finalize"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("Expected steps %v, got: %v", expected, steps)
	}

	// Verify analysis results were merged
	results := finalState["analysis_results"].(map[string]any)
	if results["status"] != "completed" || results["complexity"] != "simple" {
		t.Errorf("Expected merged analysis results, got: %v", results)
	}

	// Verify quality score
	if finalState["quality_score"] != 0.8 {
		t.Errorf("Expected quality score 0.8, got: %v", finalState["quality_score"])
	}
}

// Test Command functionality
func TestCommandSupport(t *testing.T) {
	// Test Command functionality for state update + routing
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("route_taken", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	stateGraph := graph.NewStateGraph(schema).
		AddNode("decision", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			if len(input) > 5 {
				// Use Command to both update state and route
				return &graph.Command{
					Update: graph.State{
						"route_taken": "long_path",
					},
					GoTo: "long_process",
				}, nil
			}
			// Regular state return for short path
			return graph.State{"route_taken": "short_path"}, nil
		}).
		AddNode("long_process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"result": "Long processing via Command"}, nil
		}).
		AddNode("short_process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"result": "Short processing via edges"}, nil
		}).
		SetEntryPoint("decision").
		AddEdge("decision", "short_process"). // Default edge for short path
		AddEdge("decision", "long_process").  // Add edge to make long_process reachable for validation
		SetFinishPoint("long_process").
		SetFinishPoint("short_process")

	g, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	executor, err := graph.NewExecutor(g)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Test long input (should use Command routing)
	longState := graph.State{"input": "very long input string"}
	finalState, err := executor.Invoke(context.Background(), longState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["result"] != "Long processing via Command" {
		t.Errorf("Expected Command routing result, got: %v", finalState["result"])
	}
	if finalState["route_taken"] != "long_path" {
		t.Errorf("Expected Command state update, got: %v", finalState["route_taken"])
	}

	// Test short input (should use normal edges)
	shortState := graph.State{"input": "short"}
	finalState, err = executor.Invoke(context.Background(), shortState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}

	if finalState["result"] != "Short processing via edges" {
		t.Errorf("Expected edge routing result, got: %v", finalState["result"])
	}
	if finalState["route_taken"] != "short_path" {
		t.Errorf("Expected normal state update, got: %v", finalState["route_taken"])
	}
}
