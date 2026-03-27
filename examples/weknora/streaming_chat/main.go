//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates streaming chat functionality with WeKnora agent.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/weknora"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// Get WeKnora configuration from environment variables
	weknoraBaseURL := os.Getenv("WEKNORA_BASE_URL")
	if weknoraBaseURL == "" {
		log.Fatal("WEKNORA_BASE_URL environment variable is required")
	}

	weknoraToken := os.Getenv("WEKNORA_TOKEN")
	if weknoraToken == "" {
		log.Fatal("WEKNORA_TOKEN environment variable is required")
	}

	weknoraAgentID := os.Getenv("WEKNORA_AGENT_ID")
	if weknoraAgentID == "" {
		log.Fatal("WEKNORA_AGENT_ID environment variable is required")
	}

	weknoraAgent, err := weknora.New(
		weknora.WithBaseUrl(weknoraBaseURL),
		weknora.WithToken(weknoraToken),
		weknora.WithName("weknora-streaming-assistant"),
		weknora.WithDescription("A streaming chat assistant powered by WeKnora"),
		weknora.WithAgentID(weknoraAgentID),
		weknora.WithWebSearchEnabled(true),
		weknora.WithTimeout(5*time.Minute),
	)
	if err != nil {
		log.Fatalf("Failed to create WeKnora agent: %v", err)
	}

	sessionService := inmemory.NewSessionService()

	chatRunner := runner.NewRunner(
		"weknora-streaming-runner",
		weknoraAgent,
		runner.WithSessionService(sessionService),
	)

	ctx := context.Background()
	userID := "streaming-user"
	sessionID := "streaming-session-1"

	fmt.Println("🚀 Starting WeKnora Streaming Chat Example")
	fmt.Println("Type 'exit' or 'quit' to end the conversation.")
	fmt.Println(strings.Repeat("=", 60))

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n👤 You: ")
		if !scanner.Scan() {
			break
		}
		userMessage := strings.TrimSpace(scanner.Text())
		if userMessage == "" {
			continue
		}
		if strings.ToLower(userMessage) == "exit" || strings.ToLower(userMessage) == "quit" {
			break
		}

		fmt.Print("🤖 Assistant: ")

		startTime := time.Now()

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

		var (
			chunkCount int
			totalChars int
		)

		for event := range events {
			if event.Error != nil {
				log.Printf("\nEvent error: %s", event.Error.Message)
				continue
			}

			if event.Response != nil && len(event.Response.Choices) > 0 {
				choice := event.Response.Choices[0]

				if event.Response.IsPartial {
					// Streaming chunk
					chunkCount++
					if choice.Delta.Content != "" {
						fmt.Print(choice.Delta.Content)
						totalChars += len(choice.Delta.Content)
					}
				}
			}
		}

		duration := time.Since(startTime)

		fmt.Printf("\n\n📊 Response Stats:")
		fmt.Printf("\n   • Duration: %v", duration)
		fmt.Printf("\n   • Chunks: %d", chunkCount)
		fmt.Printf("\n   • Characters: %d", totalChars)
		if duration > 0 {
			charsPerSec := float64(totalChars) / duration.Seconds()
			fmt.Printf("\n   • Speed: %.1f chars/sec", charsPerSec)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✨ Streaming chat example completed!")
}
