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
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
	flag.Parse()

	fmt.Printf("Using configuration:\n")
	fmt.Printf("- Model Name: %s\n", *modelName)
	fmt.Printf("- Channel Buffer Size: 512\n")
	fmt.Printf("- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a new OpenAI-like model instance.
	// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
	llm := openai.New(*modelName, openai.Options{
		ChannelBufferSize: 512, // Custom buffer size for high-throughput scenarios.
	})

	ctx := context.Background()

	fmt.Println("=== Non-streaming Example ===")
	if err := nonStreamingExample(ctx, llm, *modelName); err != nil {
		log.Printf("Non-streaming example failed: %v", err)
	}

	fmt.Println("\n=== Streaming Example ===")
	if err := streamingExample(ctx, llm, *modelName); err != nil {
		log.Printf("Streaming example failed: %v", err)
	}

	fmt.Println("\n=== Advanced Example with Parameters ===")
	if err := advancedExample(ctx, llm, *modelName); err != nil {
		log.Printf("Advanced example failed: %v", err)
	}

	fmt.Println("\n=== Parameter Testing Example ===")
	if err := parameterTestingExample(ctx, llm, *modelName); err != nil {
		log.Printf("Parameter testing example failed: %v", err)
	}

	fmt.Println("=== Demo Complete ===")
}

// nonStreamingExample demonstrates non-streaming usage.
func nonStreamingExample(ctx context.Context, llm *openai.Model, modelName string) error {
	temperature := 0.7
	maxTokens := 1000

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("Tell me a short joke about programming."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			fmt.Printf("Response: %s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Usage != nil {
			fmt.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d\n",
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.TotalTokens)
		}

		if response.Done {
			break
		}
	}

	return nil
}

// streamingExample demonstrates streaming usage.
func streamingExample(ctx context.Context, llm *openai.Model, modelName string) error {
	temperature := 0.8
	maxTokens := 500

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a creative storyteller."),
			model.NewUserMessage("Write a short story about a robot learning to paint."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      true,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Print("Streaming response: ")
	var fullContent string

	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}

			if choice.FinishReason != nil {
				fmt.Printf("\nFinish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Done {
			fmt.Printf("\n\nStreaming completed. Full content length: %d characters\n", len(fullContent))
			break
		}
	}

	return nil
}

// advancedExample demonstrates advanced parameters and conversation.
func advancedExample(ctx context.Context, llm *openai.Model, modelName string) error {
	temperature := 0.3
	maxTokens := 1000
	topP := 0.9

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are an expert software engineer with deep knowledge of Go programming."),
			model.NewUserMessage("Explain the benefits of using channels in Go for concurrency."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			TopP:        &topP,
			Stream:      false,
		},
	}

	fmt.Println("Sending advanced request with custom parameters:")
	fmt.Printf("- Temperature: %.1f\n", temperature)
	fmt.Printf("- Max tokens: %d\n", maxTokens)
	fmt.Printf("- Top P: %.1f\n", topP)
	fmt.Println()

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			fmt.Printf("Advanced Response:\n%s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *choice.FinishReason)
			}
		}

		// Display response metadata.
		fmt.Printf("Response ID: %s\n", response.ID)
		fmt.Printf("Model: %s\n", response.Model)
		fmt.Printf("Created: %s\n", time.Unix(response.Created, 0).Format(time.RFC3339))

		if response.Usage != nil {
			fmt.Printf("Token usage - Prompt: %d, Completion: %d, Total: %d\n",
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.TotalTokens)
		}

		if response.Done {
			break
		}
	}

	return nil
}

// parameterTestingExample demonstrates various parameter combinations.
func parameterTestingExample(ctx context.Context, llm *openai.Model, modelName string) error {
	fmt.Println("Testing different parameter combinations...")

	tests := []struct {
		name        string
		temperature *float64
		maxTokens   *int
		topP        *float64
		description string
	}{
		{
			name:        "creative",
			temperature: floatPtr(1.2),
			maxTokens:   intPtr(200),
			topP:        floatPtr(0.95),
			description: "High creativity settings",
		},
		{
			name:        "balanced",
			temperature: floatPtr(0.7),
			maxTokens:   intPtr(150),
			topP:        floatPtr(0.9),
			description: "Balanced settings",
		},
		{
			name:        "conservative",
			temperature: floatPtr(0.2),
			maxTokens:   intPtr(100),
			topP:        floatPtr(0.8),
			description: "Conservative settings",
		},
	}

	for _, test := range tests {
		fmt.Printf("\n--- %s (%s) ---\n", test.name, test.description)

		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage("You are a helpful assistant."),
				model.NewUserMessage("Write a haiku about technology."),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: test.temperature,
				MaxTokens:   test.maxTokens,
				TopP:        test.topP,
				Stream:      false,
			},
		}

		if err := testRequest(ctx, llm, request, test.description); err != nil {
			fmt.Printf("Test '%s' failed: %v\n", test.name, err)
		}
	}

	return nil
}

// testRequest sends a request and displays the response.
func testRequest(ctx context.Context, llm *openai.Model, request *model.Request, description string) error {
	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			fmt.Printf("Response: %s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Usage != nil {
			fmt.Printf("Tokens used: %d\n", response.Usage.TotalTokens)
		}

		if response.Done {
			break
		}
	}

	return nil
}

// Helper functions for creating pointers to primitive types.
func floatPtr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}
