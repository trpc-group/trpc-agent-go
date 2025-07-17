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

// Package main demonstrates how to use the LLMAgent implementation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
	flag.Parse()

	fmt.Printf("Creating LLMAgent with configuration:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a model instance.
	// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: 50, // Larger buffer for agent use.
	})

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	name := "demo-llm-agent"
	// Create an LLMAgent with configuration.
	llmAgent := llmagent.New(
		name,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant for demonstrations"),
		llmagent.WithInstruction("Be helpful, concise, and informative in your responses"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(20),
	)

	// Create an invocation context.
	invocation := &agent.Invocation{
		AgentName:     name,
		InvocationID:  "demo-invocation-001",
		EndInvocation: false,
		Model:         modelInstance,
		Message:       model.NewUserMessage("Hello! Can you tell me about yourself?"),
		Session: &session.Session{
			ID: "session-001",
		},
	}

	// Run the agent.
	ctx := context.Background()
	eventChan, err := llmAgent.Run(ctx, invocation)
	if err != nil {
		log.Fatalf("Failed to run LLMAgent: %v", err)
	}

	fmt.Println("\n=== LLMAgent Execution ===")
	fmt.Println("Processing events from LLMAgent:")

	// Process events from the agent.
	eventCount := 0
	for event := range eventChan {
		eventCount++

		fmt.Printf("\n--- Event %d ---\n", eventCount)
		fmt.Printf("ID: %s\n", event.ID)
		fmt.Printf("Author: %s\n", event.Author)
		fmt.Printf("InvocationID: %s\n", event.InvocationID)

		if event.Error != nil {
			fmt.Printf("Error: %s (Type: %s)\n", event.Error.Message, event.Error.Type)
		}

		if len(event.Choices) > 0 {
			choice := event.Choices[0]
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

	if eventCount == 0 {
		fmt.Println("No events were generated. This might indicate:")
		fmt.Println("- Model configuration issues")
		fmt.Println("- Network connectivity problems")
		fmt.Println("- Check the logs for more details")
	}

	fmt.Println("=== Demo Complete ===")
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 {
	return &f
}
