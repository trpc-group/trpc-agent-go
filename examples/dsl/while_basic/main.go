package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Register builtin components
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// while_basic demonstrates how builtin.while can be used together with
// builtin.llmagent where the loop condition is driven purely by the agent's
// structured_output:
//
//   - loop_agent produces: {step_index, continue_loop, message}
//   - builtin.while's condition is input.output_parsed.continue_loop == true
//   - builtin.end reads nodes.loop_agent.output_parsed.* to build the final result.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("🚀 DSL While Example (LLM-driven condition)")
	fmt.Println("==================================================")
	fmt.Println()

	// Step 1: Register model
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	modelRegistry := registry.NewModelRegistry()
	modelClient := openai.New(
		modelName,
		openai.WithAPIKey(apiKey),
	)
	modelRegistry.MustRegister(modelName, modelClient)
	fmt.Printf("✅ Model registered: %s\n", modelName)
	fmt.Println()

	// Step 2: Load graph definition
	data, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	var graphDef dsl.Graph
	if err := json.Unmarshal(data, &graphDef); err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Printf("✅ Loaded graph: %s\n", graphDef.Name)
	fmt.Printf("   Description: %s\n", graphDef.Description)
	fmt.Printf("   Nodes: %d\n", len(graphDef.Nodes))
	fmt.Println()

	// Step 3: Compile graph
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry)

	compiledGraph, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")
	fmt.Println()

	// Step 4: Create GraphAgent and Runner
	graphAgent, err := graphagent.New("while-basic", compiledGraph,
		graphagent.WithDescription("While-loop DSL example using LLMAgent structured_output as loop guard"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()
	appRunner := runner.NewRunner(
		"while-basic-graph",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	defer appRunner.Close()

	// Step 5: Run a single example input
	userID := "demo-user"
	sessionID := "demo-session"
	input := "Give me a few short ideas for improving developer productivity. You are allowed to iterate a few times and then stop."

	fmt.Printf("🔄 Running graph with input: %q\n\n", input)
	if err := executeGraph(ctx, appRunner, userID, sessionID, input); err != nil {
		return err
	}

	return nil
}

func executeGraph(ctx context.Context, appRunner runner.Runner, userID, sessionID, userInput string) error {
	msg := model.NewUserMessage(userInput)
	events, err := appRunner.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var (
		lastResponse string
		endOutput    map[string]any
	)

	for ev := range events {
		if ev.Error != nil {
			return fmt.Errorf("graph error: %s", ev.Error.Message)
		}

		if ev.StateDelta == nil {
			continue
		}

		// Log per-iteration structured_output from loop_agent, if present.
		if raw, ok := ev.StateDelta["node_structured"]; ok {
			var ns map[string]any
			if err := json.Unmarshal(raw, &ns); err == nil {
				if nodeOut, ok := ns["loop_agent"].(map[string]any); ok {
					if op, ok := nodeOut["output_parsed"].(map[string]any); ok {
						var (
							stepIndex    int
							continueLoop bool
							message      string
						)

						if v, ok := op["step_index"].(float64); ok {
							stepIndex = int(v)
						}
						if v, ok := op["continue_loop"].(bool); ok {
							continueLoop = v
						}
						if v, ok := op["message"].(string); ok {
							message = v
						}

						fmt.Printf("[while][loop_agent] step=%d continue_loop=%v\n", stepIndex, continueLoop)
						if message != "" {
							fmt.Printf("  message: %s\n", message)
						}
						fmt.Println()
					}
				}
			}
		}

		// Capture end_structured_output for final summary.
		if raw, ok := ev.StateDelta["end_structured_output"]; ok {
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err == nil {
				endOutput = out
			}
		}

		// Capture last_response as a textual fallback.
		if raw, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				lastResponse = s
			}
		}
	}

	fmt.Println("📋 Final Result")
	fmt.Println("==================================================")
	if endOutput != nil {
		b, _ := json.MarshalIndent(endOutput, "", "  ")
		fmt.Println(string(b))
	} else if lastResponse != "" {
		fmt.Println(lastResponse)
	} else {
		fmt.Println("<none>")
	}
	fmt.Println()

	return nil
}
