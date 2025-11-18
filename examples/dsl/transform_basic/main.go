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
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// A minimal example showing how builtin.transform can reshape user_input into
// a structured object and make it available to downstream nodes.

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("🚀 Transform DSL Example")
	fmt.Println("==================================================")
	fmt.Println()

	// Load graph definition from JSON
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

	// Compile graph
	compiler := dsl.NewCompiler(registry.DefaultRegistry)

	compiledGraph, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")
	fmt.Println()

	// Create GraphAgent
	graphAgent, err := graphagent.New("transform-basic", compiledGraph,
		graphagent.WithDescription("Demonstrates builtin.transform reshaping user_input"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create Runner
	appRunner := runner.NewRunner("transform-basic-graph", graphAgent)
	defer appRunner.Close()

	// Run a single example input
	userID := "demo-user"
	sessionID := "demo-session"
	input := "Hello from transform_basic graph"

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
		transformResult any
		endOutput       map[string]any
		lastResponse    string
	)

	for ev := range events {
		if ev.Error != nil {
			return fmt.Errorf("workflow error: %s", ev.Error.Message)
		}

		if ev.StateDelta != nil {
			// Read transform result from state.result
			if raw, ok := ev.StateDelta["result"]; ok {
				var v any
				if err := json.Unmarshal(raw, &v); err == nil {
					transformResult = v
				}
			}

			// Read structured end output if present
			if raw, ok := ev.StateDelta["end_structured_output"]; ok {
				var v map[string]any
				if err := json.Unmarshal(raw, &v); err == nil {
					endOutput = v
				}
			}

			// Fallback text view via last_response
			if raw, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					lastResponse = s
				}
			}
		}
	}

	fmt.Println("📊 Transform Result (state.result)")
	fmt.Println("==================================================")
	if transformResult != nil {
		b, _ := json.MarshalIndent(transformResult, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println("<nil>")
	}
	fmt.Println()

	fmt.Println("📋 End Structured Output")
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
