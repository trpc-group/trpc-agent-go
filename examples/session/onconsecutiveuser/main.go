//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to handle consecutive user messages.
// When network issues or race conditions cause consecutive user messages
// without an assistant response, handlers can fix the session history.
//
// This example shows three strategies:
//  1. Insert placeholder: Add a placeholder assistant message
//  2. Remove previous: Delete the first user message
//  3. Skip current: Don't append the second user message
//
// Usage:
//
//	go run . -handler=placeholder -session=inmemory
//	go run . -handler=remove -session=inmemory
//	go run . -handler=skip -session=inmemory
//
// Environment variables:
//
//	MODEL_NAME: model name (default: deepseek-chat)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName   = flag.String("model", os.Getenv("MODEL_NAME"), "model name")
	handlerType = flag.String("handler", "placeholder", "placeholder / remove / skip")
)

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return "deepseek-chat"
}

func main() {
	flag.Parse()

	mdl := getModelName()
	fmt.Printf("Using model: %s\n", mdl)
	fmt.Printf("Handler type: %s\n\n", *handlerType)

	// Select handler based on type.
	var handler session.OnConsecutiveUserMessageFunc
	switch *handlerType {
	case "placeholder":
		handler = InsertPlaceholderHandler()
		fmt.Println("[Using InsertPlaceholderHandler]")
	case "remove":
		handler = RemovePreviousHandler()
		fmt.Println("[Using RemovePreviousHandler]")
	case "skip":
		handler = SkipCurrentHandler()
		fmt.Println("[Using SkipCurrentHandler]")
	default:
		log.Fatalf("Unknown handler type: %s", *handlerType)
	}

	// Create session service with the handler.
	sessionService := sessioninmemory.NewSessionService(
		sessioninmemory.WithOnConsecutiveUserMessage(handler),
	)

	llmAgent := llmagent.New(
		"test-assistant",
		llmagent.WithModel(openai.New(mdl)),
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

	// Step 1: Normal first request.
	fmt.Println("\n=== Step 1: Normal first request ===")
	if err := chat(r, userID, sessionID, "Hello, my name is Alice", "req-1"); err != nil {
		log.Fatalf("Step 1 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 2: Simulate consecutive user messages (network issue / retry).
	fmt.Println("\n=== Step 2: Simulate consecutive user messages ===")
	if err := simulateConsecutiveUserMessages(sessionService, userID, sessionID); err != nil {
		log.Fatalf("Step 2 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 3: Normal request after fixing.
	fmt.Println("\n=== Step 3: Normal request after fixing ===")
	if err := chat(r, userID, sessionID, "What is my name?", "req-3"); err != nil {
		log.Fatalf("Step 3 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)
}

const appName = "consecutive-user-demo"

func printSessionEvents(svc session.Service, userID, sessionID string) {
	ctx := context.Background()
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		fmt.Printf("GetSession error: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Println("Session not found")
		return
	}
	fmt.Printf("--- Session Events (count=%d) ---\n", len(sess.Events))
	for i, evt := range sess.Events {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			role := evt.Response.Choices[0].Message.Role
			content := evt.Response.Choices[0].Message.Content
			if len(content) > 50 {
				content = content[:50] + "..."
			}
			fmt.Printf("[%d] %s: %s\n", i, role, content)
		}
	}
}

func chat(r runner.Runner, userID, sessionID, message, requestID string) error {
	ctx := context.Background()
	eventChan, err := r.Run(
		ctx, userID, sessionID,
		model.NewUserMessage(message),
		agent.WithRequestID(requestID),
	)
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

func simulateConsecutiveUserMessages(svc session.Service, userID, sessionID string) error {
	ctx := context.Background()
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	// Get existing session.
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	// Simulate sending consecutive user messages (connection interrupted).
	userMsg1 := &event.Event{
		Response: &model.Response{
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			Timestamp: time.Now(),
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "How are you?",
					},
				},
			},
		},
		RequestID:    "req-2a",
		InvocationID: uuid.New().String(),
		Author:       userID,
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		Version:      event.CurrentVersion,
	}

	userMsg2 := &event.Event{
		Response: &model.Response{
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			Timestamp: time.Now().Add(1 * time.Second),
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "Are you there?",
					},
				},
			},
		},
		RequestID:    "req-2b",
		InvocationID: uuid.New().String(),
		Author:       userID,
		ID:           uuid.New().String(),
		Timestamp:    time.Now().Add(1 * time.Second),
		Version:      event.CurrentVersion,
	}

	fmt.Printf("Appending first user message: %s\n", userMsg1.Response.Choices[0].Message.Content)
	if err := svc.AppendEvent(ctx, sess, userMsg1); err != nil {
		return fmt.Errorf("failed to append first user message: %w", err)
	}

	fmt.Printf("Appending second user message: %s\n", userMsg2.Response.Choices[0].Message.Content)
	if err := svc.AppendEvent(ctx, sess, userMsg2); err != nil {
		return fmt.Errorf("failed to append second user message: %w", err)
	}

	fmt.Println("[Handler triggered and fixed the consecutive user messages]")
	return nil
}
