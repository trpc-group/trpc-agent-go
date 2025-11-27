// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package main demonstrates using trpc-agent-go's a2aagent to connect to an ADK A2A server.
// This is Scenario 2: trpc-agent-go a2aagent client → ADK A2A Server
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	agentURL = flag.String("url", "http://localhost:8081", "ADK A2A agent server URL")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("trpc-agent-go A2A Agent Client")
	fmt.Println("========================================")
	fmt.Printf("Connecting to ADK A2A server: %s\n", *agentURL)
	fmt.Println("========================================")
	fmt.Println()

	// Create A2A agent client
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(*agentURL),
	)
	if err != nil {
		log.Fatalf("Failed to create A2A agent: %v", err)
	}

	// Display agent card info
	card := a2aAgent.GetAgentCard()
	fmt.Printf("Connected to agent:\n")
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
	sessionID := "test_session"

	fmt.Printf("Client Session Info:\n")
	fmt.Printf("  User ID: %s\n", userID)
	fmt.Printf("  Session ID: %s\n", sessionID)
	fmt.Println()

	// Test 1: Simple query
	fmt.Println("Test 1: Simple Query")
	fmt.Println("--------------------")
	testQuery(ctx, agentRunner, userID, sessionID, "What is the capital of France?")
	fmt.Println()

	// Test 2: Another query
	fmt.Println("Test 2: Another Query")
	fmt.Println("---------------------")
	testQuery(ctx, agentRunner, userID, sessionID, "Tell me a short joke.")
	fmt.Println()

	// Test 3: Tool calling query
	fmt.Println("Test 3: Tool Calling Query")
	fmt.Println("--------------------------")
	testQuery(ctx, agentRunner, userID, sessionID, "What is 123 + 456? And what time is it now?")
	fmt.Println()

}

func testQuery(ctx context.Context, agentRunner runner.Runner, userID, sessionID, query string) {
	fmt.Printf("Query: %s\n\n", query)

	events, err := agentRunner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(query),
		agent.WithRuntimeState(map[string]any{"test": "value"}),
	)
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}

	if err := processResponse(events); err != nil {
		log.Printf("Error processing response: %v", err)
	}
}

// processResponse prints tool activity in real time but only renders the final
// assistant message once. ADK's A2A implementation has a quirk: intermediate events
// contain clean cumulative content, but the final event often duplicates content
// or prepends the user question. To work around this, we only capture content from
// non-final events and display it when the final event arrives.
func processResponse(events <-chan *event.Event) error {
	var lastValidContent string

	for evt := range events {
		if evt.Error != nil {
			return fmt.Errorf("event error: %s", evt.Error.Message)
		}

		// Handle tool calls
		if handleToolCalls(evt) {
			continue
		}

		// Handle tool responses
		if handleToolResponses(evt) {
			continue
		}

		// Capture assistant content from intermediate (non-final) events only.
		// ADK's final event often contains duplicated or malformed content.
		if !evt.IsFinalResponse() {
			if content := captureFinalContent(evt); content != "" {
				lastValidContent = content
			}
		}

		// Print content when we receive the final response event
		if evt.IsFinalResponse() {
			if lastValidContent != "" {
				fmt.Println("🤖 Assistant:")
				fmt.Println(lastValidContent)
			}
			break
		}
	}

	return nil
}

func handleToolCalls(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	choice := evt.Response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		fmt.Println("\n🔧 Tool calls initiated:")
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Println("\n🔄 Executing tools...")
		return true
	}
	return false
}

func handleToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			content := choice.Message.Content
			if content == "" {
				content = choice.Delta.Content
			}
			if content != "" {
				fmt.Printf("✅ Tool response (ID: %s): %s\n", choice.Message.ToolID, content)
			}
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// captureFinalContent extracts assistant text from either the full message
// (non-streaming) or the delta payload (streaming). ADK's A2A bridge always
// emits the complete content in the final chunk, so whichever field is
// populated here represents the authoritative assistant answer.
// Only capture content from assistant role messages, skip tool/user messages.
func captureFinalContent(evt *event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}

	choice := evt.Response.Choices[0]

	// Only capture assistant messages, skip tool responses
	if choice.Message.Role != model.RoleAssistant && choice.Message.Role != "" {
		return ""
	}

	content := choice.Message.Content
	if content == "" {
		content = choice.Delta.Content
	}
	return content
}
