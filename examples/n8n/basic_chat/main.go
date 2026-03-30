//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates basic chat functionality with an n8n webhook agent.
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

	// Build agent options
	opts := []n8n.Option{
		n8n.WithWebhookURL(webhookURL),
		n8n.WithName("n8n-chat-assistant"),
		n8n.WithDescription("A helpful chat assistant powered by n8n"),
		n8n.WithEnableStreaming(false),
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

	// Create n8n agent
	n8nAgent, err := n8n.New(opts...)
	if err != nil {
		log.Fatalf("Failed to create n8n agent: %v", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	chatRunner := runner.NewRunner(
		"n8n-chat-runner",
		n8nAgent,
		runner.WithSessionService(sessionService),
	)

	// Example conversation
	ctx := context.Background()
	userID := "example-user"
	sessionID := "chat-session-1"

	// Test messages
	testMessages := []string{
		"Hello! Can you introduce yourself?",
		"What can you help me with?",
		"Tell me a short joke",
		"What's the weather like today?",
	}

	fmt.Println("Starting n8n Chat Example")
	fmt.Println(strings.Repeat("=", 50))

	for i, userMessage := range testMessages {
		fmt.Printf("\nUser: %s\n", userMessage)
		fmt.Print("Assistant: ")

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

		// Process events
		var response string
		for event := range events {
			if event.Error != nil {
				log.Printf("Event error: %s", event.Error.Message)
				continue
			}

			if event.Response != nil && len(event.Response.Choices) > 0 {
				choice := event.Response.Choices[0]
				if event.Response.Done {
					response = choice.Message.Content
				}
			}
		}

		if response != "" {
			fmt.Println(response)
		} else {
			fmt.Println("(No response received)")
		}

		// Add a small delay between messages
		if i < len(testMessages)-1 {
			time.Sleep(1 * time.Second)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("Chat example completed!")
}
