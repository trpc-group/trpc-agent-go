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
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/huggingface"
)

func main() {
	// Create a HuggingFace model instance
	// You can use any model available on HuggingFace Inference API
	// For example: "meta-llama/Llama-2-7b-chat-hf", "mistralai/Mistral-7B-Instruct-v0.2"
	m, err := huggingface.New(
		"meta-llama/Llama-2-7b-chat-hf",
		huggingface.WithAPIKey("your-huggingface-api-key"), // Or set HUGGINGFACE_API_KEY env var
		huggingface.WithBaseURL("https://api-inference.huggingface.co"),
	)
	if err != nil {
		log.Fatalf("Failed to create HuggingFace model: %v", err)
	}

	// Print model info
	info := m.Info()
	fmt.Printf("Using model: %s\n\n", info.Name)

	// Create a chat request
	request := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleSystem,
				Content: "You are a helpful assistant.",
			},
			{
				Role:    model.RoleUser,
				Content: "What is the capital of France?",
			},
		},
		Temperature: floatPtr(0.7),
		MaxTokens:   intPtr(100),
		Stream:      false,
	}

	// Generate content
	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		log.Fatalf("Failed to generate content: %v", err)
	}

	// Read and print responses
	for resp := range responseChan {
		if resp.Error != nil {
			log.Printf("Error in response: %s", resp.Error.Message)
			continue
		}

		if len(resp.Choices) > 0 {
			choice := resp.Choices[0]
			fmt.Printf("Assistant: %s\n", choice.Message.Content)
			
			if choice.FinishReason != nil {
				fmt.Printf("Finish reason: %s\n", *choice.FinishReason)
			}
		}

		if resp.Usage != nil {
			fmt.Printf("\nToken usage:\n")
			fmt.Printf("  Prompt tokens: %d\n", resp.Usage.PromptTokens)
			fmt.Printf("  Completion tokens: %d\n", resp.Usage.CompletionTokens)
			fmt.Printf("  Total tokens: %d\n", resp.Usage.TotalTokens)
		}
	}
}

func floatPtr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}
