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

// Package main demonstrates how to use the OpenAI-like model with environment variables.
//
// This example showcases three different interaction patterns with the LLM:
// 1. Non-streaming.
// 2. Streaming generation with user-supplied tool calls *during* execution.
// 3. Streaming input/output with incremental updates.
//
// The focus is on illustrating how to construct a `model.Request`, attach
// custom tools (created with `function.NewFunctionTool`), and consume the
// resulting `model.Response` or streaming channel. The example also provides
// guidance on configuring the model via environment variables or command-line
// flags, masking API keys in logs, and parsing the streamed chunks.
//
// Run the example with (assuming the repo root):
//
//	go run ./examples/tool -model=gpt-4o-mini
//
// Make sure to export `OPENAI_API_KEY` (and optionally `OPENAI_BASE_URL`) in
// your environment beforehand. The program prints configuration parameters,
// executes each demo function, and displays both intermediate and final
// results so that you can observe how the LLM interface behaves under
// different streaming strategies.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// main is the entry point of the application.
// It demonstrates three different interaction patterns with the LLM.
func main() {
	// Read configuration from environment variables.
	baseURL := getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	apiKey := getEnv("OPENAI_API_KEY", "")

	// Read configuration from command line flags.
	modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
	flag.Parse()

	// Validate required environment variables.
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	fmt.Printf("Using configuration:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Channel Buffer Size: 512\n")
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and " +
		"OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a new OpenAI-like model instance using the new package structure.
	llm := openai.New(*modelName, openai.Options{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})

	ctx := context.Background()

	// Execute non-streaming example.
	fmt.Println("=== Non-streaming Example ===")
	if err := nonStreamingExample(ctx, llm); err != nil {
		log.Printf("Non-streaming example failed: %v", err)
	}

	// Execute streaming input example.
	fmt.Println("=== Streaming Input Example ===")
	if err := streamingInputExample(ctx, llm); err != nil {
		log.Printf("Streaming Input example failed: %v", err)
	}

	// Execute streaming output example.
	fmt.Println("=== Streaming Output Example ===")
	if err := streamingOutputExample(ctx, llm); err != nil {
		log.Printf("Streaming Output example failed: %v", err)
	}
}

// getEnv gets an environment variable with a default value.
// If the environment variable is not set, it returns the default value.
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// maskAPIKey masks the API key for logging purposes.
// It returns the first 3 characters followed by asterisks for security.
func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 6 {
		return "***"
	}
	return apiKey[:3]
}
