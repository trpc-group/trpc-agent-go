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
	// Create a HuggingFace model instance with streaming callbacks
	m, err := huggingface.New(
		"meta-llama/Llama-2-7b-chat-hf",
		huggingface.WithAPIKey("your-huggingface-api-key"),
		// Add callbacks to monitor the streaming process
		huggingface.WithChatRequestCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest) {
			fmt.Println("📤 Sending request to HuggingFace API...")
		}),
		huggingface.WithChatChunkCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, chunk *huggingface.ChatCompletionChunk) {
			// This is called for each chunk received
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				fmt.Print(chunk.Choices[0].Delta.Content)
			}
		}),
		huggingface.WithChatStreamCompleteCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, err error) {
			if err != nil {
				fmt.Printf("\n❌ Stream completed with error: %v\n", err)
			} else {
				fmt.Println("\n✅ Stream completed successfully")
			}
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create HuggingFace model: %v", err)
	}

	// Create a streaming chat request
	request := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleSystem,
				Content: "You are a helpful assistant that provides detailed explanations.",
			},
			{
				Role:    model.RoleUser,
				Content: "Explain how neural networks work in simple terms.",
			},
		},
		Temperature: floatPtr(0.7),
		MaxTokens:   intPtr(500),
		Stream:      true, // Enable streaming
	}

	// Generate content with streaming
	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		log.Fatalf("Failed to generate content: %v", err)
	}

	fmt.Println("🤖 Assistant: ")
	
	// Read streaming responses
	var totalTokens int
	for resp := range responseChan {
		if resp.Error != nil {
			log.Printf("Error in response: %s", resp.Error.Message)
			continue
		}

		// Track token usage if available
		if resp.Usage != nil {
			totalTokens = resp.Usage.TotalTokens
		}
	}

	if totalTokens > 0 {
		fmt.Printf("\n\n📊 Total tokens used: %d\n", totalTokens)
	}
}

func floatPtr(f float64) *float64 {
	return &f
}

func intPtr(i int) *int {
	return &i
}
