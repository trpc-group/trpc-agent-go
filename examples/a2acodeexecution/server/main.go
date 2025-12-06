//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an A2A server with code execution capabilities.
// This server exposes an LLM agent that can execute Python code via the A2A protocol.
package main

import (
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
	host      = flag.String("host", "0.0.0.0:8888", "A2A server host address")
	streaming = flag.Bool("streaming", true, "Enable streaming mode")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("A2A Code Execution Server")
	fmt.Println("========================================")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Host: %s\n", *host)
	fmt.Printf("Streaming: %v\n", *streaming)
	fmt.Println("========================================")
	fmt.Println()

	// Create model instance
	modelInstance := openai.New(*modelName)

	// Create generation config
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}

	// Create LLM agent with code execution capability
	codeAgent := llmagent.New(
		"code_execution_agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An agent that can execute Python code to solve problems"),
		llmagent.WithInstruction(codeExecutionInstruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithCodeExecutor(local.New()), // Enable local code execution
	)

	// Create A2A server
	server, err := a2a.New(
		a2a.WithHost(*host),
		a2a.WithAgent(codeAgent, *streaming),
		a2a.WithDebugLogging(false),
	)
	if err != nil {
		log.Fatalf("Failed to create A2A server: %v", err)
	}

	// Start server (blocking)
	fmt.Printf("Starting A2A server on %s...\n", *host)
	fmt.Println("Press Ctrl+C to stop the server")
	fmt.Println()

	if err := server.Start(*host); err != nil {
		log.Fatalf("A2A server error: %v", err)
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// codeExecutionInstruction is the system instruction for code execution agent.
const codeExecutionInstruction = `You are a helpful assistant that can execute Python code to solve problems.

When you need to perform calculations, data analysis, or any task that requires code execution:
1. Write Python code enclosed in triple backticks with the language identifier
2. The code will be executed automatically and you will see the results
3. Use the results to provide your final answer

Example:
` + "```python" + `
# Calculate sum
result = sum(range(1, 11))
print(f"The sum is: {result}")
` + "```" + `

Important guidelines:
- Always use print() to display results
- Handle errors gracefully
- Do not use external packages that need installation (pip install)
- Available libraries: math, statistics, json, datetime, collections, itertools, functools, re
- Provide clear explanations along with your code
`
