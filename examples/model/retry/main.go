//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use model retry mechanism in trpc-agent-go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
	maxRetries := flag.Int("retries", 3, "Maximum number of retries")
	timeout := flag.Duration("timeout", 30*time.Second, "Request timeout")
	flag.Parse()

	fmt.Printf("🚀 Using configuration:\n")
	fmt.Printf("   📝 Model Name: %s\n", *modelName)
	fmt.Printf("   🔄 Max Retries: %d\n", *maxRetries)
	fmt.Printf("   ⏱️ Request Timeout: %v\n", *timeout)
	fmt.Printf("   🔑 OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment\n")
	fmt.Println()

	// Create a new OpenAI-like model instance with retry configuration.
	// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
	llm := openai.New(*modelName,
		openai.WithOpenAIOptions(
			openaiopt.WithMaxRetries(*maxRetries),
			openaiopt.WithRequestTimeout(*timeout),
		),
	)

	ctx := context.Background()

	fmt.Println("🔄 === Basic Retry Example ===")
	if err := basicRetryExample(ctx, llm); err != nil {
		log.Printf("❌ Basic retry example failed: %v", err)
	}

	fmt.Println("\n⚡ === Advanced Retry Example ===")
	if err := advancedRetryExample(ctx, llm); err != nil {
		log.Printf("❌ Advanced retry example failed: %v", err)
	}

	fmt.Println("\n🌊 === Streaming with Retry Example ===")
	if err := streamingWithRetryExample(ctx, llm); err != nil {
		log.Printf("❌ Streaming with retry example failed: %v", err)
	}

	fmt.Println("\n🚦 === Rate Limiting Retry Example ===")
	if err := rateLimitingRetryExample(ctx, llm); err != nil {
		log.Printf("❌ Rate limiting retry example failed: %v", err)
	}

	fmt.Println("🎉 === Demo Complete ===")
}

// basicRetryExample demonstrates basic retry configuration.
func basicRetryExample(ctx context.Context, llm *openai.Model) error {
	fmt.Println("💬 Sending basic request...")

	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Hello, how are you?"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
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
			fmt.Printf("🤖 Response: %s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("🏁 Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Done {
			break
		}
	}

	return nil
}

// advancedRetryExample demonstrates advanced retry configuration with custom parameters.
func advancedRetryExample(ctx context.Context, llm *openai.Model) error {
	maxTokens := 100
	temperature := 0.7

	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Explain quantum computing in simple terms."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		},
	}

	fmt.Println("🔬 Sending advanced request with custom parameters:")
	fmt.Printf("   📊 Max tokens: %d\n", maxTokens)
	fmt.Printf("   🌡️  Temperature: %.1f\n", temperature)
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
			fmt.Printf("🧠 Advanced Response:\n%s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("🏁 Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Usage != nil {
			fmt.Printf("💎 Token usage - Prompt: %d, Completion: %d, Total: %d\n",
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

// streamingWithRetryExample demonstrates streaming with retry configuration.
func streamingWithRetryExample(ctx context.Context, llm *openai.Model) error {
	fmt.Println("🌊 Starting streaming request...")

	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Write a short poem about AI."),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Print("📝 Streaming response: ")
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
				fmt.Printf("\n🏁 Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Done {
			fmt.Printf("\n\n✅ Streaming completed. Full content length: %d characters\n", len(fullContent))
			break
		}
	}

	return nil
}

// rateLimitingRetryExample demonstrates how retry mechanism handles rate limiting scenarios.
func rateLimitingRetryExample(ctx context.Context, llm *openai.Model) error {
	fmt.Println("🚦 Testing retry mechanism for potential rate limiting scenarios...")

	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("This request might hit rate limits."),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
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
			fmt.Printf("🤖 Response: %s\n", choice.Message.Content)

			if choice.FinishReason != nil {
				fmt.Printf("🏁 Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if response.Done {
			break
		}
	}

	return nil
}
