//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn chat using Runner with multiple
// session backends. It highlights how to switch between sessions while the
// agent keeps per-session context.
//
// Usage:
//
//	go run main.go -session=inmemory
//	go run main.go -session=redis
//	go run main.go -session=postgres
//	go run main.go -session=mysql
//	go run main.go -session=clickhouse
//
// Environment variables by session type (example usage):
//
//	redis:
//		export REDIS_ADDR="localhost:6379"
//
//	postgres:
//		export PG_HOST="localhost"
//		export PG_PORT="5432"
//		export PG_USER="postgres"
//		export PG_PASSWORD="password"
//		export PG_DATABASE="trpc_agent"
//
//	mysql:
//		export MYSQL_HOST="localhost"
//		export MYSQL_PORT="3306"
//		export MYSQL_USER="root"
//		export MYSQL_PASSWORD="password"
//		export MYSQL_DATABASE="trpc_agent"
//
//	clickhouse:
//		export CLICKHOUSE_HOST="localhost"
//		export CLICKHOUSE_PORT="9000"
//		export CLICKHOUSE_USER="default"
//		export CLICKHOUSE_PASSWORD=""
//		export CLICKHOUSE_DATABASE="trpc_agent"
package main

import (
	"bufio"
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
	modelName       = flag.String("model", os.Getenv("MODEL_NAME"), "Name of the model to use (default: MODEL_NAME env var)")
	sessServiceName = flag.String("session", "inmemory", "Name of the session service to use, inmemory / redis / postgres / mysql / clickhouse")
	streaming       = flag.Bool("streaming", true, "Enable streaming mode for responses")
	eventLimit      = flag.Int("event-limit", 1000, "Maximum number of events to store per session")
	sessionTTL      = flag.Duration("session-ttl", 10*time.Second, "Session time-to-live duration")
	debugMode       = flag.Bool("debug", true, "Enable debug mode to print session events after each turn")
)

func main() {
	flag.Parse()

	fmt.Printf("Session Management Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Event Limit: %d\n", *eventLimit)
	fmt.Printf("Session TTL: %v\n", *sessionTTL)
	fmt.Printf("Session Backend: %s\n", *sessServiceName)
	fmt.Printf("Debug Mode: %t\n", *debugMode)
	fmt.Println(strings.Repeat("=", 50))

	chat := &multiTurnChat{
		modelName: *modelName,
		streaming: *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type multiTurnChat struct {
	modelName      string
	streaming      bool
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

// run starts the interactive chat session.
func (c *multiTurnChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

func (c *multiTurnChat) setup(_ context.Context) error {
	// Create session service using util.
	sessionType := util.SessionType(*sessServiceName)
	sessionService, err := util.NewSessionServiceByType(sessionType, util.SessionServiceConfig{
		EventLimit: *eventLimit,
		TTL:        *sessionTTL,
	})
	if err != nil {
		return fmt.Errorf("failed to create session service: %w", err)
	}
	c.sessionService = sessionService

	modelInstance := openai.New(c.modelName)

	genConfig := model.GenerationConfig{
		Stream: c.streaming,
	}

	llmAgent := llmagent.New(
		"session-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant demonstrating session management capabilities."),
		llmagent.WithInstruction("You are demonstrating multi-session conversation capabilities. "+
			"Remember context within each session and engage naturally with users. "+
			"ONLY when users explicitly ask about conversation history (e.g., 'show history', 'what did we discuss'), "+
			"display the complete original conversation history in a clear list format:\n"+
			"1. Show each exchange with clear user/assistant labels\n"+
			"2. Present the exact original messages without summarization\n"+
			"3. Use numbered lists for clarity\n"+
			"4. Maintain chronological order\n"+
			"Otherwise, respond naturally to user queries without repeating history."),
		llmagent.WithGenerationConfig(genConfig),
	)

	c.runner = runner.NewRunner(
		"session-demo",
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	c.userID = "user"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Printf("Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Session commands:")
	fmt.Println("   /history   - Ask the assistant to recap our conversation")
	fmt.Println("   /new       - Start a brand-new session ID")
	fmt.Println("   /sessions  - List known session IDs")
	fmt.Println("   /use <id>  - Switch to an existing (or new) session")
	fmt.Println("   /exit      - End the conversation")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		lowerInput := strings.ToLower(userInput)

		switch {
		case lowerInput == "/exit":
			fmt.Println("Goodbye!")
			return nil
		case lowerInput == "/history":
			userInput = "show our conversation history"
		case lowerInput == "/new":
			c.startNewSession()
			continue
		case lowerInput == "/sessions":
			c.listSessions()
			continue
		case strings.HasPrefix(lowerInput, "/use"):
			target := strings.TrimSpace(userInput[4:])
			if target == "" {
				fmt.Println("Usage: /use <session-id>")
				continue
			}
			c.switchSession(target)
			continue
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}

		// Print session events in debug mode.
		if *debugMode {
			if err := util.PrintSessionEvents(ctx, c.sessionService, "session-demo", c.userID, c.sessionID); err != nil {
				fmt.Printf("Debug error: %v\n", err)
			}
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	requestID := uuid.New().String()
	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		// Do not break on tool response events (Done=true but not final assistant response).
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func (c *multiTurnChat) handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\nError: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls.
	if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses.
	if c.handleToolResponses(event) {
		return nil
	}

	// Handle content.
	c.handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func (c *multiTurnChat) handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("Tool calls initiated:\n")
		for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   - %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\nExecuting tools...\n")
		return true
	}
	return false
}

// handleToolResponses detects and displays tool responses.
func (c *multiTurnChat) handleToolResponses(event *event.Event) bool {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		hasToolResponse := false
		for _, choice := range event.Response.Choices {
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("Tool response (ID: %s): %s\n",
					choice.Message.ToolID,
					strings.TrimSpace(choice.Message.Content))
				hasToolResponse = true
			}
		}
		if hasToolResponse {
			return true
		}
	}
	return false
}

// handleContent processes and displays content.
func (c *multiTurnChat) handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		content := c.extractContent(choice)

		if content != "" {
			c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
		}
	}
}

// extractContent extracts content based on streaming mode.
func (c *multiTurnChat) extractContent(choice model.Choice) string {
	if c.streaming {
		// Streaming mode: use delta content.
		return choice.Delta.Content
	}
	// Non-streaming mode: use full message content.
	return choice.Message.Content
}

// displayContent prints content to console.
func (c *multiTurnChat) displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\nAssistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}

func (c *multiTurnChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

func (c *multiTurnChat) listSessions() {
	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "session-demo",
		UserID:  c.userID,
	}
	sessions, err := c.sessionService.ListSessions(ctx, userKey)
	if err != nil {
		fmt.Printf("Failed to list sessions: %v\n\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Println("(no sessions recorded yet)")
		fmt.Println()
		return
	}
	fmt.Println("Session roster:")
	for _, sess := range sessions {
		marker := " "
		if sess.ID == c.sessionID {
			marker = "*"
		}
		fmt.Printf("   %s %s (updated: %s)\n", marker, sess.ID, sess.UpdatedAt.Format(time.RFC3339))
	}
	fmt.Println()
}

func (c *multiTurnChat) switchSession(target string) {
	target = strings.TrimSpace(target)
	if target == "" {
		fmt.Println("Usage: /use <session-id>")
		return
	}
	if target == c.sessionID {
		fmt.Printf("Already using session %s\n", target)
		return
	}
	c.sessionID = target
	fmt.Printf("Switched to session %s\n", target)
}
