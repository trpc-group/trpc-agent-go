//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the OpenAI-compatible model with the
// Requesty router (https://router.requesty.ai/v1).
//
// Requesty exposes an OpenAI-compatible Chat Completions API, so it works with
// the existing model/openai client by pointing the base URL at the Requesty
// router and supplying a Requesty API key. Models are addressed with the
// provider/model naming convention, e.g. "openai/gpt-4o-mini".
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// defaultRequestyBaseURL is the OpenAI-compatible Requesty router endpoint.
const defaultRequestyBaseURL = "https://router.requesty.ai/v1"

func main() {
	// Read configuration from command line flags.
	modelName := flag.String("model", "openai/gpt-4o-mini", "Name of the model to use (provider/model)")
	baseURL := flag.String("base-url", defaultRequestyBaseURL, "Requesty router base URL")
	flag.Parse()

	// Requesty authenticates with a bearer key passed via REQUESTY_API_KEY.
	apiKey := os.Getenv("REQUESTY_API_KEY")
	if apiKey == "" {
		log.Fatal("REQUESTY_API_KEY is not set. Get a key at https://app.requesty.ai/api-keys")
	}

	fmt.Printf("🚀 Using configuration:\n")
	fmt.Printf("   📝 Model Name: %s\n", *modelName)
	fmt.Printf("   🌐 Base URL: %s\n", *baseURL)
	fmt.Printf("   🔑 API key sourced from REQUESTY_API_KEY\n")
	fmt.Println()

	// Create an OpenAI-compatible model instance pointed at the Requesty router.
	// WithBaseURL and WithAPIKey are passed explicitly so the example works
	// without relying on OPENAI_BASE_URL / OPENAI_API_KEY.
	llm := openai.New(*modelName,
		openai.WithBaseURL(*baseURL),
		openai.WithAPIKey(apiKey),
	)

	ctx := context.Background()

	fmt.Println("🔄 === Non-streaming Example ===")
	if err := nonStreamingExample(ctx, llm); err != nil {
		log.Printf("❌ Non-streaming example failed: %v", err)
	}

	fmt.Println("\n🌊 === Streaming Example ===")
	if err := streamingExample(ctx, llm); err != nil {
		log.Printf("❌ Streaming example failed: %v", err)
	}

	fmt.Println("🎉 === Demo Complete ===")
}

// nonStreamingExample demonstrates non-streaming usage against Requesty.
func nonStreamingExample(ctx context.Context, llm *openai.Model) error {
	fmt.Println("💬 Sending non-streaming request...")

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
			fmt.Printf("🤖 Response: %s\n", choice.Message.Content)

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

// streamingExample demonstrates streaming usage against Requesty.
func streamingExample(ctx context.Context, llm *openai.Model) error {
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
