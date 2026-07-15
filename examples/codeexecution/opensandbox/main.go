//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the OpenSandbox code execution
// capabilities with LLMAgent. This example uses DeepSeek as the LLM
// and OpenSandbox (running in Docker Desktop WSL2) as the code executor.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/opensandbox"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("Creating LLMAgent with OpenSandbox code executor:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Code Executor: OpenSandbox (Docker Desktop WSL2)\n")
	fmt.Println()

	// DeepSeek requires its own base URL to trigger the DeepSeek variant.
	// OPENAI_API_KEY is read from env; set it to the DeepSeek API key.
	modelInstance := openai.New(*modelName,
		openai.WithBaseURL("https://api.deepseek.com"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	// OpenSandbox executor. Endpoint and API key come from env vars
	// (or defaults for local Docker Desktop WSL2 setup).
	endpoint := os.Getenv("OPENSANDBOX_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:8080"
	}
	apiKey := os.Getenv("OPENSANDBOX_API_KEY")
	if apiKey == "" {
		apiKey = "test-key-1234"
	}
	executorOpts := []opensandbox.Option{
		opensandbox.WithDomain(endpoint),
		opensandbox.WithProtocol("http"),
		opensandbox.WithAPIKey(apiKey),
		// WSL2 / Docker Desktop: sandbox containers live on a bridge
		// network that the host cannot reach directly.
		opensandbox.WithUseServerProxy(true),
		opensandbox.WithExecutionTimeout(60 * time.Second),
	}
	executor, err := opensandbox.New(executorOpts...)
	if err != nil {
		log.Fatalf("Failed to create OpenSandbox executor: %v", err)
	}
	defer func() {
		if cerr := executor.Close(); cerr != nil {
			log.Printf("Failed to close executor: %v", cerr)
		}
	}()

	name := "opensandbox_data_agent"
	llmAgent := llmagent.New(
		name,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("agent for data science tasks running in an OpenSandbox sandbox"),
		llmagent.WithInstruction(`You are a data assistant. Your code is executed inside an isolated OpenSandbox sandbox.

Your workflow:
1. Understand the user's data analysis request.
2. When computation is needed, reply with exactly one runnable fenced `+"`python`"+` code block and no surrounding prose, explanations, or additional text. The framework will auto-execute this block via WithCodeExecutor.
3. Print the results so they appear in the execution output.
4. After the execution output is available, summarize the findings for the user.

Constraints:
- You should NEVER install any package on your own like pip install .... The sandbox may not have internet access.
- Only use Python's standard library.
- Keep code snippets short and focused.
`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithCodeExecutor(executor),
	)

	r := runner.NewRunner(
		"opensandbox_data_agent",
		llmAgent,
	)
	defer r.Close()

	query := "analyze some sample data: 5, 12, 8, 15, 7, 9, 11. " +
		"Compute the mean, median, variance and standard deviation using only Python's standard library."

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	eventChan, err := r.Run(ctx, "user-id", "session-id", model.NewUserMessage(query))
	if err != nil {
		log.Printf("Failed to run LLMAgent: %v", err)
		return
	}

	fmt.Println("\n=== LLMAgent with OpenSandbox Execution ===")
	fmt.Println("Processing events from LLMAgent:")

	eventCount := 0
	for event := range eventChan {
		eventCount++

		fmt.Printf("\n--- Event %d ---\n", eventCount)
		fmt.Printf("ID: %s\n", event.ID)
		fmt.Printf("Author: %s\n", event.Author)
		fmt.Printf("InvocationID: %s\n", event.InvocationID)
		fmt.Printf("Object: %s\n", event.Object)

		if event.Error != nil {
			fmt.Printf("Error: %s (Type: %s)\n", event.Error.Message, event.Error.Type)
		}

		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			if choice.Message.Content != "" {
				fmt.Printf("Message Content: %s\n", choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Printf("Delta Content: %s\n", choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				fmt.Printf("Finish Reason: %s\n", *choice.FinishReason)
			}
		}

		if event.Usage != nil {
			fmt.Printf("Token Usage - Prompt: %d, Completion: %d, Total: %d\n",
				event.Usage.PromptTokens,
				event.Usage.CompletionTokens,
				event.Usage.TotalTokens)
		}

		fmt.Printf("Done: %t\n", event.Done)

		if event.Done {
			break
		}
	}

	fmt.Printf("\n=== Execution Complete ===\n")
	fmt.Printf("Total events processed: %d\n", eventCount)
	fmt.Println("=== Demo Complete ===")
}

func intPtr(i int) *int { return &i }

func floatPtr(f float64) *float64 { return &f }
