//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates streaming chat functionality with an n8n webhook agent.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/n8n"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// Get n8n configuration from environment variables
	webhookURL := os.Getenv("N8N_WEBHOOK_URL")
	if webhookURL == "" {
		log.Fatal("N8N_WEBHOOK_URL environment variable is required")
	}

	apiKey := os.Getenv("N8N_API_KEY")

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

	// Build agent options
	opts := []n8n.Option{
		n8n.WithWebhookURL(webhookURL),
		n8n.WithName("n8n-streaming-assistant"),
		n8n.WithDescription("A streaming chat assistant powered by n8n"),
		n8n.WithEnableStreaming(true),
		n8n.WithStreamingRespHandler(streamingHandler),
		n8n.WithStreamingChannelBufSize(2048),
	}

	// Add header authentication if API key is provided
	if apiKey != "" {
		opts = append(opts,
			n8n.WithAuthType(n8n.AuthHeader),
			n8n.WithAuthConfig(&n8n.AuthConfig{
				HeaderName:  "Authorization",
				HeaderValue: "Bearer " + apiKey,
			}),
		)
	}

	// Create n8n agent with streaming enabled
	n8nAgent, err := n8n.New(opts...)
	if err != nil {
		log.Fatalf("Failed to create n8n agent: %v", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	chatRunner := runner.NewRunner(
		"n8n-streaming-runner",
		n8nAgent,
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

	fmt.Println("Starting n8n Streaming Chat Example")
	fmt.Println(strings.Repeat("=", 60))

	for i, userMessage := range testMessages {
		fmt.Printf("\nUser: %s\n", userMessage)
		fmt.Print("Assistant: ")

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

		fmt.Printf("\n\nResponse Stats:")
		fmt.Printf("\n   Duration: %v", duration)
		fmt.Printf("\n   Chunks: %d", chunkCount)
		fmt.Printf("\n   Characters: %d", totalChars)
		if duration > 0 {
			charsPerSec := float64(totalChars) / duration.Seconds()
			fmt.Printf("\n   Speed: %.1f chars/sec", charsPerSec)
		}

		// Verify content consistency
		if finalResponse != "" && aggregatedContent.String() != finalResponse {
			fmt.Printf("\n   Warning: Content mismatch detected:")
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
	fmt.Println("Streaming chat example completed!")
}
