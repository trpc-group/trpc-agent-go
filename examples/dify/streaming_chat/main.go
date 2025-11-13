//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates streaming chat functionality with Dify agent.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	difySDK "github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/dify"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// Get Dify configuration from environment variables
	difyBaseURL := os.Getenv("DIFY_BASE_URL")
	if difyBaseURL == "" {
		difyBaseURL = "https://api.dify.ai/v1" // Default Dify API URL
	}

	difyAPISecret := os.Getenv("DIFY_API_SECRET")
	if difyAPISecret == "" {
		log.Fatal("DIFY_API_SECRET environment variable is required")
	}

	// Custom streaming response handler that processes chunks
	streamingHandler := func(resp *model.Response) (string, error) {
		if len(resp.Choices) > 0 {
			content := resp.Choices[0].Delta.Content
			// Print each chunk as it arrives (for real-time effect)
			if content != "" {
				fmt.Print(content)
			}
			return content, nil
		}
		return "", nil
	}

	// Create Dify agent with streaming enabled
	difyAgent, err := dify.New(
		dify.WithBaseUrl(difyBaseURL),
		dify.WithName("dify-streaming-assistant"),
		dify.WithDescription("A streaming chat assistant powered by Dify"),
		dify.WithEnableStreaming(true), // Enable streaming
		dify.WithStreamingRespHandler(streamingHandler),
		dify.WithStreamingChannelBufSize(2048), // Larger buffer for streaming
		// WithGetDifyClientFunc enables custom client configuration per invocation
		// For streaming: often need longer timeouts and specific connection settings
		// The invocation parameter provides access to user context and session data
		dify.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*difySDK.Client, error) {
			// Configure client optimized for streaming responses
			return difySDK.NewClientWithConfig(&difySDK.ClientConfig{
				Host:             difyBaseURL,      // Dify streaming endpoint
				DefaultAPISecret: difyAPISecret,    // API authentication
				Timeout:          60 * time.Second, // Extended timeout for streaming
			}), nil
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create Dify agent: %v", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	chatRunner := runner.NewRunner(
		"dify-streaming-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	// Example conversation for streaming
	ctx := context.Background()
	userID := "streaming-user"
	sessionID := "streaming-session-1"

	// Test messages that work well with streaming
	testMessages := []string{
		"Please write a short story about a robot learning to paint",
		"Explain how machine learning works in simple terms",
		"Give me a recipe for chocolate chip cookies with detailed steps",
	}

	fmt.Println("🚀 Starting Dify Streaming Chat Example")
	fmt.Println(strings.Repeat("=", 60))

	for i, userMessage := range testMessages {
		fmt.Printf("\n👤 User: %s\n", userMessage)
		fmt.Print("🤖 Assistant: ")

		// Track the start time for response timing
		startTime := time.Now()

		// Run the agent
		events, err := chatRunner.Run(
			ctx,
			userID,
			sessionID,
			model.NewUserMessage(userMessage),
		)
		if err != nil {
			log.Printf("Error running agent: %v", err)
			continue
		}

		// Process streaming events
		var (
			aggregatedContent strings.Builder
			chunkCount        int
			finalResponse     string
		)

		for event := range events {
			if event.Error != nil {
				log.Printf("Event error: %s", event.Error.Message)
				continue
			}

			if event.Response != nil && len(event.Response.Choices) > 0 {
				choice := event.Response.Choices[0]

				if event.Response.IsPartial {
					// Streaming chunk
					chunkCount++
					if choice.Delta.Content != "" {
						aggregatedContent.WriteString(choice.Delta.Content)
					}
				} else if event.Response.Done {
					// Final response
					finalResponse = choice.Message.Content
				}
			}
		}

		// Calculate response metrics
		duration := time.Since(startTime)
		totalChars := aggregatedContent.Len()

		fmt.Printf("\n\n📊 Response Stats:")
		fmt.Printf("\n   • Duration: %v", duration)
		fmt.Printf("\n   • Chunks: %d", chunkCount)
		fmt.Printf("\n   • Characters: %d", totalChars)
		if duration > 0 {
			charsPerSec := float64(totalChars) / duration.Seconds()
			fmt.Printf("\n   • Speed: %.1f chars/sec", charsPerSec)
		}

		// Verify content consistency
		if finalResponse != "" && aggregatedContent.String() != finalResponse {
			fmt.Printf("\n⚠️  Content mismatch detected:")
			fmt.Printf("\n   Streamed: %d chars", aggregatedContent.Len())
			fmt.Printf("\n   Final: %d chars", len(finalResponse))
		}

		// Add separator between messages
		if i < len(testMessages)-1 {
			fmt.Println("\n" + strings.Repeat("-", 40))
			time.Sleep(2 * time.Second)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✨ Streaming chat example completed!")
}
