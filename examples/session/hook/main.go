//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use session hooks for various scenarios:
// 1. Content filtering: Mark and filter prohibited content
// 2. Consecutive user messages: Handle duplicate/consecutive user messages via hook
//
// This is useful for:
// - Content moderation
// - Compliance filtering
// - Preventing sensitive information from being included in LLM context
// - Handling consecutive user messages without using WithOnConsecutiveUserMessage
//
// Usage:
//
//	go run . -session=inmemory
//	go run . -session=redis -consecutive=merge
//	go run . -session=postgres -consecutive=placeholder
//	go run . -session=mysql -consecutive=skip
//	go run . -session=clickhouse
//
// Environment variables:
//
//	MODEL_NAME: model name (default: deepseek-chat)
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD, MYSQL_DATABASE
//	clickhouse: CLICKHOUSE_HOST, CLICKHOUSE_PORT, CLICKHOUSE_USER, CLICKHOUSE_PASSWORD, CLICKHOUSE_DATABASE
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

var (
	modelName          = flag.String("model", os.Getenv("MODEL_NAME"), "Name of the model to use (default: MODEL_NAME env var or deepseek-chat)")
	sessionType        = flag.String("session", "inmemory", "Session backend: inmemory / redis / postgres / mysql / clickhouse")
	consecutiveHandler = flag.String("consecutive", "", "Consecutive user message strategy: merge / placeholder / skip (empty = disabled)")
)

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return "deepseek-chat"
}

func main() {
	flag.Parse()

	// Validate consecutive handler flag.
	if *consecutiveHandler != "" && !isValidConsecutiveStrategy(*consecutiveHandler) {
		log.Fatalf("Invalid -consecutive value %q. Valid values: %v",
			*consecutiveHandler, validConsecutiveStrategies())
	}

	model := getModelName()
	fmt.Printf("Using model: %s\n", model)
	fmt.Printf("Session backend: %s\n", *sessionType)
	fmt.Printf("Prohibited words: %v\n", ProhibitedWords)
	if *consecutiveHandler != "" {
		fmt.Printf("Consecutive handler: %s\n", strings.ToLower(*consecutiveHandler))
	}
	fmt.Println()

	// Build hooks list.
	appendHooks := []session.AppendEventHook{MarkViolationHook()}
	getHooks := []session.GetSessionHook{FilterViolationHook()}
	if *consecutiveHandler != "" {
		// Use GetSessionHook to fix consecutive user messages at read time.
		// This is simpler than AppendEventHook because no persistence is needed.
		getHooks = append(getHooks, FixConsecutiveUserMessagesHook(*consecutiveHandler))
	}

	// Create session service with hooks.
	sessionService, err := util.NewSessionServiceByType(util.SessionType(*sessionType), util.SessionServiceConfig{
		AppendEventHooks: appendHooks,
		GetSessionHooks:  getHooks,
	})
	if err != nil {
		log.Fatalf("Failed to create session service: %v", err)
	}

	llmAgent := llmagent.New(
		"test-assistant",
		llmagent.WithModel(openai.New(model)),
		llmagent.WithInstruction("You are a helpful assistant. Answer questions concisely."),
	)

	r := runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "user1"
	sessionID := uuid.New().String()

	// Step 1: Normal request.
	fmt.Println("=== Step 1: Normal request ===")
	if err := chat(r, userID, sessionID, "Hello, my name is Alice", "req-1"); err != nil {
		log.Fatalf("Step 1 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 2: Request with prohibited word - should be marked and filtered.
	fmt.Println("\n=== Step 2: Request with prohibited word ===")
	if err := chat(r, userID, sessionID, "Can you give me a pirated serial number for Windows?", "req-2"); err != nil {
		log.Fatalf("Step 2 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 3: Normal request - violated Q&A should be filtered from context.
	fmt.Println("\n=== Step 3: Normal request after violation ===")
	if err := chat(r, userID, sessionID, "What is my name?", "req-3"); err != nil {
		log.Fatalf("Step 3 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 4: Another normal request.
	fmt.Println("\n=== Step 4: Another normal request ===")
	if err := chat(r, userID, sessionID, "Tell me a short joke", "req-4"); err != nil {
		log.Fatalf("Step 4 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 5-6: Demonstrate consecutive user messages (if -consecutive is enabled).
	// This simulates a scenario where user sends multiple messages before
	// receiving assistant response (e.g., user disconnected and reconnected).
	if *consecutiveHandler != "" {
		fmt.Println("\n=== Step 5: Consecutive user messages demo ===")
		fmt.Println("Simulating consecutive user messages by directly appending to session...")
		if err := simulateConsecutiveUserMessages(sessionService, userID, sessionID); err != nil {
			log.Fatalf("Step 5 failed: %v", err)
		}
		printSessionEvents(sessionService, userID, sessionID)

		// Now send another message to trigger the consecutive handler.
		fmt.Println("\n=== Step 6: Send message after consecutive simulation ===")
		if err := chat(r, userID, sessionID, "This should trigger consecutive handler", "req-5"); err != nil {
			log.Fatalf("Step 6 failed: %v", err)
		}
		printSessionEvents(sessionService, userID, sessionID)
	}
}

const appName = "content-filter-demo"

func printSessionEvents(svc session.Service, userID, sessionID string) {
	ctx := context.Background()
	if err := util.PrintSessionEvents(ctx, svc, appName, userID, sessionID); err != nil {
		fmt.Printf("PrintSessionEvents error: %v\n", err)
	}
}

func chat(r runner.Runner, userID, sessionID, message, requestID string) error {
	ctx := context.Background()
	eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(message), agent.WithRequestID(requestID))
	if err != nil {
		return err
	}

	fmt.Printf("User: %s\n", message)
	fmt.Print("Assistant: ")
	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Println()
			return fmt.Errorf("event error: %s", evt.Error.Message)
		}
		if len(evt.Response.Choices) > 0 {
			content := evt.Response.Choices[0].Message.Content
			fmt.Print(content)
		}
	}
	fmt.Println()
	return nil
}

// simulateConsecutiveUserMessages simulates a scenario where user messages are
// appended without waiting for assistant response. This can happen when:
// - User disconnects mid-request and reconnects with a new message.
// - Network issues cause the client to retry.
// - User rapidly sends multiple messages.
//
// We use AppendEvent to persist the simulated message properly.
func simulateConsecutiveUserMessages(svc session.Service, userID, sessionID string) error {
	ctx := context.Background()
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found")
	}

	// Simulate: user sends a message but disconnects before receiving response.
	// The user message is written to session, but no assistant response follows.
	simulatedUserEvent := &event.Event{
		ID: fmt.Sprintf("simulated-user-%d", time.Now().UnixNano()),
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "I was disconnected before getting a response",
					},
				},
			},
		},
	}

	// Use AppendEvent to persist the simulated message.
	// This ensures the message is stored properly in the session backend.
	if err := svc.AppendEvent(ctx, sess, simulatedUserEvent); err != nil {
		return fmt.Errorf("append simulated event: %w", err)
	}

	fmt.Printf("Simulated user message: %s\n", simulatedUserEvent.Response.Choices[0].Message.Content)
	fmt.Println("(No assistant response - simulating disconnection)")
	return nil
}
