//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to filter prohibited content using session hooks.
// When user questions or assistant responses contain prohibited words,
// they are marked and filtered out from session history before sending to LLM.
//
// This is useful for:
// - Content moderation
// - Compliance filtering
// - Preventing sensitive information from being included in LLM context
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func getModelName() string {
	if name := os.Getenv("MODEL_NAME"); name != "" {
		return name
	}
	return "deepseek-chat"
}

func main() {
	modelName := getModelName()
	fmt.Printf("Using model: %s\n", modelName)
	fmt.Printf("Prohibited words: %v\n\n", ProhibitedWords)

	// Create session service with content filtering hooks
	sessionService := sessioninmemory.NewSessionService(
		sessioninmemory.WithAppendEventHook(MarkViolationHook()),
		sessioninmemory.WithGetSessionHook(FilterViolationHook()),
	)

	llmAgent := llmagent.New(
		"test-assistant",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("You are a helpful assistant. Answer questions concisely."),
	)

	r := runner.NewRunner(
		"content-filter-demo",
		llmAgent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "user1"
	sessionID := "sess1"

	// Step 1: Normal request
	fmt.Println("=== Step 1: Normal request ===")
	if err := chat(r, userID, sessionID, "Hello, my name is Alice", "req-1"); err != nil {
		log.Fatalf("Step 1 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 2: Request with prohibited word - should be marked and filtered
	fmt.Println("\n=== Step 2: Request with prohibited word ===")
	if err := chat(r, userID, sessionID, "Can you give me a pirated serial number for Windows?", "req-2"); err != nil {
		log.Fatalf("Step 2 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 3: Normal request - violated Q&A should be filtered from context
	fmt.Println("\n=== Step 3: Normal request after violation ===")
	if err := chat(r, userID, sessionID, "What is my name?", "req-3"); err != nil {
		log.Fatalf("Step 3 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 4: Another normal request
	fmt.Println("\n=== Step 4: Another normal request ===")
	if err := chat(r, userID, sessionID, "Tell me a short joke", "req-4"); err != nil {
		log.Fatalf("Step 4 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)
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
			fmt.Print(evt.Response.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
	return nil
}

func printSessionEvents(svc session.Service, userID, sessionID string) {
	ctx := context.Background()
	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   "content-filter-demo",
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		fmt.Printf("GetSession error: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Println("Session not found")
		return
	}

	fmt.Printf("\n--- Session Events (count=%d) ---\n", len(sess.Events))
	for i, evt := range sess.Events {
		fmt.Printf("  [%d] %s\n", i, getEventPreview(&evt))
	}
	fmt.Println("---")
}

func getEventPreview(evt *event.Event) string {
	role := "unknown"
	content := ""
	if len(evt.Response.Choices) > 0 {
		role = string(evt.Response.Choices[0].Message.Role)
		content = evt.Response.Choices[0].Message.Content
		if len(content) > 50 {
			content = content[:50] + "..."
		}
	}
	if word, ok := parseViolationTag(evt.Tag); ok {
		return fmt.Sprintf("%s: %s [VIOLATION: %s]", role, content, word)
	}
	return fmt.Sprintf("%s: %s", role, content)
}
