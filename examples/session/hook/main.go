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
//
// Usage:
//
//	go run . -session=inmemory
//	go run . -session=redis
//	go run . -session=postgres
//	go run . -session=mysql
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

var (
	modelName   = flag.String("model", os.Getenv("MODEL_NAME"), "Name of the model to use (default: MODEL_NAME env var or deepseek-chat)")
	sessionType = flag.String("session", "inmemory", "Session backend: inmemory / redis / postgres / mysql / clickhouse")
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
	fmt.Printf("Prohibited words: %v\n\n", ProhibitedWords)

	// Create session service with content filtering hooks
	sessionService, err := util.NewSessionServiceByType(util.SessionType(*sessionType), util.SessionServiceConfig{
		AppendEventHooks: []session.AppendEventHook{MarkViolationHook()},
		GetSessionHooks:  []session.GetSessionHook{FilterViolationHook()},
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
