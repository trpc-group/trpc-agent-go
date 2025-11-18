//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import to register builtin components
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("🚀 Code Execution DSL Example")
	fmt.Println("==================================================")
	fmt.Println()

	// Step 1: Register custom components
	componentRegistry := registry.DefaultRegistry
	componentRegistry.MustRegister(&FormatResultsComponent{})

	// Step 2: Load graph definition from JSON
	graphData, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	var graphDef dsl.Graph
	if err := json.Unmarshal(graphData, &graphDef); err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Printf("✅ Loaded graph: %s\n", graphDef.Name)
	fmt.Printf("   Description: %s\n", graphDef.Description)
	fmt.Printf("   Nodes: %d\n", len(graphDef.Nodes))
	fmt.Println()

	// Step 3: Compile graph
	compiler := dsl.NewCompiler(componentRegistry)

	compiledGraph, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	fmt.Println("✅ Graph compiled successfully!")
	fmt.Println()

	// Step 4: Create GraphAgent
	graphAgent, err := graphagent.New("code-execution-graph", compiledGraph,
		graphagent.WithDescription("DSL-based code execution graph"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Step 5: Create Runner
	appRunner := runner.NewRunner(
		"code-execution-demo",
		graphAgent,
	)
	defer appRunner.Close()

	// Step 6: Execute graph
	fmt.Println("🔄 Executing graph...")
	fmt.Println()

	userID := "demo-user"
	sessionID := "demo-session"
	message := model.NewUserMessage("Execute code graph")

	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	// Step 7: Process events and collect results
	fmt.Println("📊 Processing Events:")
	fmt.Println("==================================================")

	var pythonOutput, bashOutput, finalResult string
	eventCount := 0

	for ev := range eventChan {
		eventCount++

		// Handle errors
		if ev.Error != nil {
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}

		// Extract state updates from StateDelta
		if ev.StateDelta != nil {
			if poBytes, ok := ev.StateDelta["python_output"]; ok {
				var po string
				if err := json.Unmarshal(poBytes, &po); err == nil && po != "" {
					pythonOutput = po
				}
			}
			if boBytes, ok := ev.StateDelta["bash_output"]; ok {
				var bo string
				if err := json.Unmarshal(boBytes, &bo); err == nil && bo != "" {
					bashOutput = bo
				}
			}
			if frBytes, ok := ev.StateDelta["final_result"]; ok {
				var fr string
				if err := json.Unmarshal(frBytes, &fr); err == nil && fr != "" {
					finalResult = fr
				}
			}
		}

		if ev.Done {
			break
		}
	}

	// Step 8: Display results
	fmt.Println()
	fmt.Println("📊 Execution Results:")
	fmt.Println("==================================================")
	fmt.Printf("Total events processed: %d\n\n", eventCount)

	if pythonOutput != "" {
		fmt.Println("📝 Python Output:")
		fmt.Println(pythonOutput)
		fmt.Println()
	}

	if bashOutput != "" {
		fmt.Println("📝 Bash Output:")
		fmt.Println(bashOutput)
		fmt.Println()
	}

	if finalResult != "" {
		fmt.Println("📋 Final Result:")
		fmt.Println(finalResult)
	}

	fmt.Println()
	fmt.Println("✅ Example completed successfully!")

	return nil
}
