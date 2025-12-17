//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an A2A client that connects to a code execution server.
// It shows how to handle code execution events (code and result) via the A2A protocol.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	serverURL = flag.String("url", "http://localhost:8888", "A2A server URL")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("A2A Code Execution Client")
	fmt.Println("========================================")
	fmt.Printf("Server URL: %s\n", *serverURL)
	fmt.Println("========================================")
	fmt.Println()

	// Create A2A agent client
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(*serverURL),
	)
	if err != nil {
		log.Fatalf("Failed to create A2A agent: %v", err)
	}

	// Display agent card info
	card := a2aAgent.GetAgentCard()
	fmt.Printf("Connected to A2A Server:\n")
	fmt.Printf("  Name: %s\n", card.Name)
	fmt.Printf("  Description: %s\n", card.Description)
	fmt.Printf("  URL: %s\n", card.URL)
	fmt.Println()

	// Create session service and runner
	sessionService := inmemory.NewSessionService()
	agentRunner := runner.NewRunner("test", a2aAgent, runner.WithSessionService(sessionService))
	defer agentRunner.Close()

	ctx := context.Background()
	userID := "test_user"
	sessionID := fmt.Sprintf("session_%d", time.Now().UnixNano())

	fmt.Printf("Client Session Info:\n")
	fmt.Printf("  User ID: %s\n", userID)
	fmt.Printf("  Session ID: %s\n", sessionID)
	fmt.Println()

	// Test 1: Simple code execution
	fmt.Println("Test 1: Simple Python Code Execution")
	fmt.Println("=====================================")
	testQuery(ctx, agentRunner, userID, sessionID,
		"Calculate the sum of numbers from 1 to 10 using Python code")
	fmt.Println()

	// Test 2: Data analysis
	fmt.Println("Test 2: Data Analysis with Code")
	fmt.Println("================================")
	testQuery(ctx, agentRunner, userID, sessionID,
		"Analyze this data: [5, 12, 8, 15, 7, 9, 11]. Calculate mean, median, and standard deviation using Python.")
	fmt.Println()

	// Test 3: Multiple steps
	fmt.Println("Test 3: Code with Multiple Steps")
	fmt.Println("=================================")
	testQuery(ctx, agentRunner, userID, sessionID,
		"Create a list of fibonacci numbers up to the 10th term using Python")
	fmt.Println()
}

func testQuery(ctx context.Context, agentRunner runner.Runner, userID, sessionID, query string) {
	fmt.Printf("Query: %s\n\n", query)

	events, err := agentRunner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(query),
	)
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}

	if err := processCodeExecutionResponse(events); err != nil {
		log.Printf("Error processing response: %v", err)
	}
}

// processCodeExecutionResponse processes events and displays code execution information.
func processCodeExecutionResponse(events <-chan *event.Event) error {
	var lastValidContent string

	for evt := range events {
		if evt.Error != nil {
			return fmt.Errorf("event error: %s", evt.Error.Message)
		}

		// Handle code execution events (ObjectType == codeexecution && Tag == code)
		if handleCodeExecution(evt) {
			continue
		}

		// Handle code execution result events (ObjectType == codeexecution && Tag == code_execution_result)
		if handleCodeExecutionResult(evt) {
			continue
		}

		// Capture assistant content from intermediate (non-final) events only
		if !evt.IsFinalResponse() {
			if content := captureFinalContent(evt); content != "" {
				lastValidContent = content
			}
		}

		// Print content when we receive the final response event
		if evt.IsFinalResponse() {
			if lastValidContent != "" {
				fmt.Println("Assistant:")
				fmt.Println(lastValidContent)
			}
			break
		}
	}

	return nil
}

// handleCodeExecution processes code execution events.
// Checks: ObjectType == postprocessing.code_execution && Tag contains "code"
func handleCodeExecution(evt *event.Event) bool {
	if evt.Response == nil {
		return false
	}

	// Check ObjectType first, then Tag for code
	if evt.Response.Object == model.ObjectTypePostprocessingCodeExecution &&
		evt.ContainsTag(event.CodeExecutionTag) {
		if len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// Use Delta for streaming response, Message for non-streaming
			content := strings.TrimSpace(choice.Delta.Content)
			if content == "" {
				content = strings.TrimSpace(choice.Message.Content)
			}
			if content == "" {
				return true
			}

			fmt.Println("\n[Code Execution]")
			fmt.Println("---------------------------------------------")
			fmt.Println(content)
			fmt.Println("---------------------------------------------")
			fmt.Println()
		}
		return true
	}
	return false
}

// handleCodeExecutionResult processes code execution result events.
// Checks: ObjectType == postprocessing.code_execution && Tag contains "code_execution_result"
func handleCodeExecutionResult(evt *event.Event) bool {
	if evt.Response == nil {
		return false
	}

	// Check ObjectType first, then Tag for result
	if evt.Response.Object == model.ObjectTypePostprocessingCodeExecution &&
		evt.ContainsTag(event.CodeExecutionResultTag) {
		if len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// Use Delta for streaming response, Message for non-streaming
			content := strings.TrimSpace(choice.Delta.Content)
			if content == "" {
				content = strings.TrimSpace(choice.Message.Content)
			}
			if content == "" {
				return false
			}

			fmt.Println("[Code Execution Result]")
			fmt.Println(content)
			fmt.Println("---------------------------------------------\n")
		}
		return true
	}
	return false
}

// captureFinalContent extracts assistant text from message or delta.
func captureFinalContent(evt *event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}

	choice := evt.Response.Choices[0]

	// Only capture assistant messages, skip tool responses and code execution
	if choice.Message.Role != model.RoleAssistant && choice.Message.Role != "" {
		return ""
	}

	// Skip code execution events (both code and result have the same ObjectType)
	if evt.Response.Object == model.ObjectTypePostprocessingCodeExecution {
		return ""
	}

	content := choice.Message.Content
	if content == "" {
		content = choice.Delta.Content
	}
	return content
}
