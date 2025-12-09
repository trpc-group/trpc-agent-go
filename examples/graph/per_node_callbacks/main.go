//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates per-node callbacks functionality in the graph package.
// This example shows how to use both global and per-node callbacks for fine-grained
// control over node execution behavior.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	// Default model name for deepseek-chat.
	defaultModelName = "deepseek-chat"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	interactive = flag.Bool("interactive", false,
		"Run in interactive mode")
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🚀 Per-Node Callbacks Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the workflow.
	workflow := &perNodeCallbacksWorkflow{
		modelName: *modelName,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// perNodeCallbacksWorkflow demonstrates per-node callback functionality.
type perNodeCallbacksWorkflow struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the per-node callbacks workflow.
func (w *perNodeCallbacksWorkflow) run() error {
	ctx := context.Background()
	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer w.runner.Close()

	if *interactive {
		return w.startInteractiveMode(ctx)
	}
	return w.runDefaultExamples(ctx)
}

// setup creates the graph agent and runner.
func (w *perNodeCallbacksWorkflow) setup() error {
	// Create the workflow graph.
	workflowGraph, err := w.createWorkflowGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("per-node-callbacks-demo", workflowGraph,
		graphagent.WithDescription("Demonstration of per-node callbacks functionality"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
	w.runner = runner.NewRunner(
		"per-node-callbacks-workflow",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	// Generate session ID.
	w.userID = "user-123"
	w.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	return nil
}

// createWorkflowGraph creates the workflow graph with per-node callbacks.
func (w *perNodeCallbacksWorkflow) createWorkflowGraph() (*graph.Graph, error) {
	// Define state schema.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("step1_result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("step2_result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("final_result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	// Create global callbacks for logging and monitoring.
	globalCallbacks := graph.NewNodeCallbacks().
		RegisterBeforeNode(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
			fmt.Printf("🌍 [GLOBAL] Before node: %s (%s)\n", callbackCtx.NodeID, callbackCtx.NodeType)
			return nil, nil
		}).
		RegisterAfterNode(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
			fmt.Printf("🌍 [GLOBAL] After node: %s (%s)\n", callbackCtx.NodeID, callbackCtx.NodeType)
			return nil, nil
		}).
		RegisterOnNodeError(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, err error) {
			fmt.Printf("🌍 [GLOBAL] Error in node: %s (%s) - %v\n", callbackCtx.NodeID, callbackCtx.NodeType, err)
		})

	// Create the workflow graph with global callbacks.
	workflowGraph, err := graph.NewStateGraph(schema).
		WithNodeCallbacks(globalCallbacks).
		// Step 1: Process input with custom pre-callback that modifies input.
		AddNode("step1", w.processStep1,
			graph.WithName("Step 1 - Input Processing"),
			graph.WithDescription("Processes the input with custom pre-callback"),
			graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
				fmt.Printf("🎯 [STEP1] Pre-callback: Enhancing input before processing\n")
				// Modify input to add a prefix.
				if input, exists := state["input"]; exists {
					if inputStr, ok := input.(string); ok {
						state["input"] = "Enhanced: " + inputStr
						fmt.Printf("🎯 [STEP1] Input enhanced: %s\n", state["input"])
					}
				}
				return nil, nil
			}),
			graph.WithPostNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
				fmt.Printf("🎯 [STEP1] Post-callback: Validating step 1 result\n")
				if result != nil {
					fmt.Printf("🎯 [STEP1] Result validated successfully\n")
				}
				return nil, nil
			}),
		).
		// Step 2: Transform result with error handling callback.
		AddNode("step2", w.processStep2,
			graph.WithName("Step 2 - Result Transformation"),
			graph.WithDescription("Transforms the result with error handling"),
			graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
				fmt.Printf("🎯 [STEP2] Pre-callback: Preparing for transformation\n")
				// Check if step1 result exists.
				if step1Result, exists := state["step1_result"]; !exists || step1Result == "" {
					fmt.Printf("🎯 [STEP2] Warning: No step1 result found, using default\n")
					state["step1_result"] = "default_value"
				}
				return nil, nil
			}),
			graph.WithNodeErrorCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, err error) {
				fmt.Printf("🎯 [STEP2] Error callback: Handling step 2 error gracefully\n")
				// Set a fallback result on error.
				state["step2_result"] = "fallback_result_due_to_error"
			}),
		).
		// Step 3: Final processing with conditional callback.
		AddNode("step3", w.processStep3,
			graph.WithName("Step 3 - Final Processing"),
			graph.WithDescription("Final processing with conditional behavior"),
			graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
				fmt.Printf("🎯 [STEP3] Pre-callback: Analyzing input for final processing\n")
				// Check input length and potentially skip processing.
				if input, exists := state["input"]; exists {
					if inputStr, ok := input.(string); ok {
						if len(inputStr) > 50 {
							fmt.Printf("🎯 [STEP3] Input too long, skipping processing\n")
							// Return a custom state update to skip node execution
							// and mark a final result directly.
							return graph.State{"final_result": "skipped_due_to_length"}, nil
						}
					}
				}
				return nil, nil
			}),
			graph.WithPostNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
				fmt.Printf("🎯 [STEP3] Post-callback: Finalizing result\n")
				// Add a timestamp to the result.
				if result != nil {
					if resState, ok := result.(graph.State); ok {
						if final, ok2 := resState["final_result"].(string); ok2 && final != "" {
							resState["final_result"] = fmt.Sprintf("%s [processed at %s]", final, time.Now().Format("15:04:05"))
							return resState, nil
						}
					}
				}
				return nil, nil
			}),
		).
		// Set up the workflow edges.
		SetEntryPoint("step1").
		AddEdge("step1", "step2").
		AddEdge("step2", "step3").
		SetFinishPoint("step3").
		Compile()

	if err != nil {
		return nil, fmt.Errorf("failed to compile graph: %w", err)
	}
	return workflowGraph, nil
}

// Node functions for the workflow.

// processStep1 processes the input and returns a result.
func (w *perNodeCallbacksWorkflow) processStep1(ctx context.Context, state graph.State) (any, error) {
	input := state["input"].(string)
	fmt.Printf("📝 [STEP1] Processing input: %s\n", input)

	// Simulate some processing time.
	time.Sleep(100 * time.Millisecond)

	result := fmt.Sprintf("Processed: %s", strings.ToUpper(input))
	return graph.State{"step1_result": result}, nil
}

// processStep2 transforms the step1 result.
func (w *perNodeCallbacksWorkflow) processStep2(ctx context.Context, state graph.State) (any, error) {
	step1Result := state["step1_result"].(string)
	fmt.Printf("📝 [STEP2] Transforming: %s\n", step1Result)

	// Simulate potential error for demonstration.
	if strings.Contains(step1Result, "ERROR") {
		return nil, fmt.Errorf("simulated error in step 2")
	}

	result := fmt.Sprintf("Transformed: %s", strings.ReplaceAll(step1Result, " ", "_"))
	return graph.State{"step2_result": result}, nil
}

// processStep3 performs final processing.
func (w *perNodeCallbacksWorkflow) processStep3(ctx context.Context, state graph.State) (any, error) {
	step2Result := state["step2_result"].(string)
	fmt.Printf("📝 [STEP3] Final processing: %s\n", step2Result)

	// Simulate some processing time.
	time.Sleep(150 * time.Millisecond)

	finalResult := fmt.Sprintf("Final: %s", step2Result)
	return graph.State{"final_result": finalResult}, nil
}

// runDefaultExamples runs predefined examples.
func (w *perNodeCallbacksWorkflow) runDefaultExamples(ctx context.Context) error {
	examples := []string{
		"Hello World",
		"This is a very long input that will trigger the length check callback in step 3",
		"ERROR test input",
		"Normal processing test",
	}

	fmt.Printf("📋 Running %d examples...\n\n", len(examples))

	for i, input := range examples {
		fmt.Printf("--- Example %d ---\n", i+1)
		fmt.Printf("Input: %s\n", input)

		if err := w.processInput(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
		time.Sleep(500 * time.Millisecond) // Add delay between examples.
	}

	return nil
}

// startInteractiveMode starts the interactive mode.
func (w *perNodeCallbacksWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Interactive Per-Node Callbacks Mode")
	fmt.Println("   Enter your input to see callback behavior")
	fmt.Println("   Type 'help' for available commands")
	fmt.Println()

	for {
		fmt.Print("📝 Input: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "help":
			w.showHelp()
			continue
		}

		// Process the input.
		if err := w.processInput(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between inputs.
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processInput processes a single input through the workflow.
func (w *perNodeCallbacksWorkflow) processInput(ctx context.Context, input string) error {
	// Create user message.
	message := model.NewUserMessage(input)

	// Run the workflow through the runner.
	eventChan, err := w.runner.Run(
		ctx,
		w.userID,
		w.sessionID,
		message,
		// Set runtime state for each run.
		agent.WithRuntimeState(map[string]any{
			"user_id": w.userID,
			"input":   input,
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process streaming response.
	return w.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming workflow response.
func (w *perNodeCallbacksWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		stageCount      int
		responseStarted bool
	)

	for event := range eventChan {
		// Track node execution events via metadata regardless of author.
		if event.StateDelta != nil {
			if nodeData, exists := event.StateDelta[graph.MetadataKeyNode]; exists {
				var nodeMetadata graph.NodeExecutionMetadata
				if err := json.Unmarshal(nodeData, &nodeMetadata); err == nil {
					switch nodeMetadata.Phase {
					case graph.ExecutionPhaseStart:
						fmt.Printf("\n🚀 Entering node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
					case graph.ExecutionPhaseComplete:
						fmt.Printf("✅ Completed node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
					case graph.ExecutionPhaseError:
						fmt.Printf("❌ Error in node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
					}
				}
			}
		}

		// Handle errors after metadata so per-node error callbacks
		// can be observed via metadata.
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Process streaming content from LLM nodes.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !responseStarted {
					fmt.Print("🤖 Response: ")
					responseStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
			// Add newline when streaming is complete.
			if choice.Delta.Content == "" && responseStarted {
				fmt.Println()
				responseStarted = false
			}
		}

		// Track workflow stages.
		if event.Author == graph.AuthorGraphExecutor {
			stageCount++
			if stageCount >= 1 && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" {
					fmt.Printf("\n🔄 Stage %d completed, %s\n", stageCount, content)
				} else {
					fmt.Printf("\n🔄 Stage %d completed\n", stageCount)
				}
			}
		}

		// Handle completion and final response.
		if event.Done {
			// Check for final response in the completion event.
			if event.Response != nil && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" && !responseStarted {
					fmt.Print("🤖 Final Response: ")
					fmt.Println(content)
				}
			}

			// Check for final result in state delta.
			if event.StateDelta != nil {
				if finalResultData, exists := event.StateDelta["final_result"]; exists {
					var finalResult string
					if err := json.Unmarshal(finalResultData, &finalResult); err == nil && finalResult != "" {
						if !responseStarted {
							fmt.Print("🎯 Final Result: ")
							fmt.Println(finalResult)
						}
					}
				}
			}
			break
		}
	}

	return nil
}

// showHelp displays help information.
func (w *perNodeCallbacksWorkflow) showHelp() {
	fmt.Printf("\n📖 Available Commands:\n")
	fmt.Printf("  help     - Show this help message\n")
	fmt.Printf("  exit     - Exit the application\n")
	fmt.Printf("  quit     - Exit the application\n")
	fmt.Printf("\n💡 Example Inputs:\n")
	fmt.Printf("  - \"Hello World\" - Normal processing\n")
	fmt.Printf("  - \"This is a very long input...\" - Triggers length check\n")
	fmt.Printf("  - \"ERROR test\" - Triggers error handling\n")
	fmt.Printf("  - \"Normal processing test\" - Standard workflow\n\n")
}
