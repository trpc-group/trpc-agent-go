//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the Recursive Language Model (RLM) paradigm.
//
// RLM (arXiv:2512.24601) treats the user prompt as an external environment variable
// rather than feeding it directly into the LLM context window. The LLM writes code
// in a Starlark (Python subset) REPL to inspect, slice, and recursively analyze the
// prompt through sub-LLM calls.
//
// Architecture:
//   - Service:  HTTP server managing LLM calls and recursive RLM invocations
//   - RLM:      Core iterative loop (LLM generates code → REPL executes → observe → repeat)
//   - REPL:     Starlark sandbox with injected builtins (context, llm_query, rlm_query, FINAL)
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//   - MODEL_NAME: (Optional) Model name, defaults to gpt-4o-mini
//
// Example usage:
//
//	go run . --context-file /path/to/large/document.txt --query "Summarize the key ideas"
//	go run . --context-file doc.txt --max-depth 2 --max-iterations 20
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

var (
	contextFile   = flag.String("context-file", "", "Path to the context file (required)")
	userQuery     = flag.String("query", "", "Query to answer about the context")
	modelFlag     = flag.String("model", "", "Model name (overrides MODEL_NAME env)")
	maxIterations = flag.Int("max-iterations", 15, "Maximum RLM iterations per level")
	maxDepth      = flag.Int("max-depth", 1, "Maximum recursion depth for rlm_query")
)

func main() {
	flag.Parse()

	if *contextFile == "" {
		log.Fatal("--context-file is required. RLM is designed for processing large files.")
	}
	data, err := os.ReadFile(*contextFile)
	if err != nil {
		log.Fatalf("Failed to read context file: %v", err)
	}
	promptContext := string(data)

	modelName := getEnvOrDefault("MODEL_NAME", "gpt-4o-mini")
	if *modelFlag != "" {
		modelName = *modelFlag
	}

	query := *userQuery
	if query == "" {
		query = "Based on the provided context, identify and list all key technical concepts. For each concept, provide a one-sentence explanation."
	}

	fmt.Println("Recursive Language Model (RLM)")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Max Iterations: %d\n", *maxIterations)
	fmt.Printf("Max Depth: %d\n", *maxDepth)
	fmt.Printf("Context: %d chars, %d lines\n", len(promptContext), countLines(promptContext))
	fmt.Printf("Query: %s\n", query)
	fmt.Println(strings.Repeat("=", 50))

	svc, err := NewService(modelName, *maxDepth, *maxIterations)
	if err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}
	defer svc.Stop()
	fmt.Printf("Service listening on %s\n", svc.Address())

	ctx := context.Background()
	answer, err := svc.RunRLM(ctx, RLMQueryRequest{
		Query:   query,
		Context: promptContext,
		Depth:   0,
	})
	if err != nil {
		log.Fatalf("RLM failed: %v", err)
	}

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("FINAL ANSWER:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println(answer)
}

func getEnvOrDefault(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}
