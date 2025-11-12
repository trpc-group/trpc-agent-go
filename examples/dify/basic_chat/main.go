//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates basic chat functionality with Dify agent.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/difyagent"
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

	// Create Dify agent
	difyAgent, err := difyagent.New(
		difyagent.WithBaseUrl(difyBaseURL),
		difyagent.WithName("dify-chat-assistant"),
		difyagent.WithDescription("A helpful chat assistant powered by Dify"),
		difyagent.WithEnableStreaming(false), // Start with non-streaming for simplicity
		difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
			return dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             difyBaseURL,
				DefaultAPISecret: difyAPISecret,
				Timeout:          30 * time.Second,
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
		"dify-chat-runner",
		difyAgent,
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

	fmt.Println("🤖 Starting Dify Chat Example")
	fmt.Println(strings.Repeat("=", 50))

	for i, userMessage := range testMessages {
		fmt.Printf("\n👤 User: %s\n", userMessage)
		fmt.Print("🤖 Assistant: ")

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
	fmt.Println("✅ Chat example completed!")
}