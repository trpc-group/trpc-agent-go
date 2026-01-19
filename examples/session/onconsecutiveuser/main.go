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
//	go run . -handler=remove -session=redis
//	go run . -handler=skip -session=postgres
//	go run . -handler=skip -session=mysql
//	go run . -handler=skip -session=clickhouse
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
	modelName   = flag.String("model", os.Getenv("MODEL_NAME"), "Name of the model to use (default: MODEL_NAME env var or deepseek-chat)")
	sessionType = flag.String("session", "inmemory", "Session backend: inmemory / redis / postgres / mysql / clickhouse")
	handlerType = flag.String("handler", "placeholder", "Handler type: placeholder / remove / skip")
)

func getModelName() string {
	if *modelName != "" {
		return *modelName
	}
	return "deepseek-chat"
}

func main() {
	flag.Parse()

	model := getModelName()
	fmt.Printf("Using model: %s\n", model)
	fmt.Printf("Session backend: %s\n", *sessionType)
	fmt.Printf("Handler type: %s\n\n", *handlerType)

	// Create session service
	sessionService, err := util.NewSessionServiceByType(util.SessionType(*sessionType), util.SessionServiceConfig{})
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

	// Step 1: Normal first request
	fmt.Println("=== Step 1: Normal first request ===")
	if err := chat(r, userID, sessionID, "Hello, my name is Alice", "req-1"); err != nil {
		log.Fatalf("Step 1 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 2: Simulate duplicate user message (network issue / retry).
	fmt.Println("\n=== Step 2: Simulate duplicate user message (no assistant response) ===")
	if err := simulateDuplicateUserMessage(sessionService, userID, sessionID, *handlerType); err != nil {
		log.Fatalf("Step 2 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)

	// Step 3: Normal request after fixing
	fmt.Println("\n=== Step 3: Normal request after fixing ===")
	if err := chat(r, userID, sessionID, "What is my name?", "req-3"); err != nil {
		log.Fatalf("Step 3 failed: %v", err)
	}
	printSessionEvents(sessionService, userID, sessionID)
}

const appName = "duplicate-user-demo"

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

func simulateDuplicateUserMessage(svc session.Service, userID, sessionID, handlerType string) error {
	ctx := context.Background()

	// Select handler based on type.
	var handler session.OnConsecutiveUserMessageFunc
	switch handlerType {
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
		return fmt.Errorf("unknown handler type: %s", handlerType)
	}

	// Create a new session with the handler.
	sess := session.NewSession(
		appName,
		userID,
		sessionID,
		session.WithOnConsecutiveUserMessage(handler),
	)

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
