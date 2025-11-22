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

	// Step 2: Load workflow from JSON
	workflowData, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	var workflow dsl.Workflow
	if err := json.Unmarshal(workflowData, &workflow); err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Printf("✅ Loaded workflow: %s\n", workflow.Name)
	fmt.Printf("   Description: %s\n", workflow.Description)
	fmt.Printf("   Nodes: %d\n", len(workflow.Nodes))
	fmt.Println()

	// Step 3: Compile workflow
	compiler := dsl.NewCompiler(componentRegistry)

	compiledGraph, err := compiler.Compile(&workflow)
	if err != nil {
		return fmt.Errorf("failed to compile workflow: %w", err)
	}

	fmt.Println("✅ Workflow compiled successfully!")
	fmt.Println()

	// Step 4: Create GraphAgent
	graphAgent, err := graphagent.New("code-execution-workflow", compiledGraph,
		graphagent.WithDescription("DSL-based code execution workflow"),
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

	// Step 6: Execute workflow
	fmt.Println("🔄 Executing workflow...")
	fmt.Println()

	userID := "demo-user"
	sessionID := "demo-session"
	message := model.NewUserMessage("Execute code workflow")

	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
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
