//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to directly append events to session without
// invoking the model. This is useful for scenarios like:
// - Pre-loading conversation history
// - Inserting system messages or context
// - Recording user actions or metadata
// - Building conversation context from external sources
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
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 AppendEvent Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	chat := &appendEventChat{
		modelName: *modelName,
		streaming: *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type appendEventChat struct {
	modelName  string
	streaming  bool
	runner     runner.Runner
	sessionSvc session.Service
	userID     string
	sessionID  string
	sessionIDs []string
}

// run starts the interactive chat session.
func (c *appendEventChat) run() error {
	ctx := context.Background()

	// Setup the runner and session service.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up.
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

func (c *appendEventChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName)

	// Initialize session service (in-memory).
	c.sessionSvc = sessioninmemory.NewSessionService()

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	llmAgent := llmagent.New(
		"append-event-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant demonstrating append event capabilities."),
		llmagent.WithInstruction("You are a helpful assistant. When users ask about conversation history, "+
			"you can see all messages including those directly appended to the session."),
		llmagent.WithGenerationConfig(genConfig),
	)

	c.runner = runner.NewRunner(
		"append-event-demo",
		llmAgent,
		runner.WithSessionService(c.sessionSvc),
	)

	c.userID = "user"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	c.rememberSession(c.sessionID)

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (c *appendEventChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	c.printCommands()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle commands or process as normal message.
		handled, shouldExit, err := c.handleCommand(ctx, userInput)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		if shouldExit {
			return nil
		}
		if handled {
			fmt.Println()
			continue
		}

		// Normal chat message - process through runner.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// printCommands displays available commands.
func (c *appendEventChat) printCommands() {
	fmt.Println("💡 Commands:")
	fmt.Println("   /append <message>        - Append a user message directly to session")
	fmt.Println("   /append-system <message> - Append a system message to session")
	fmt.Println("   /append-assistant <msg>  - Append an assistant message to session")
	fmt.Println("   /events                  - List all events in current session")
	fmt.Println("   /new                     - Start a brand-new session ID")
	fmt.Println("   /sessions                - List known session IDs")
	fmt.Println("   /use <id>                - Switch to an existing (or new) session")
	fmt.Println("   /exit                    - End the conversation")
	fmt.Println()
}

// handleCommand processes user commands.
// Returns: (handled, shouldExit, error)
func (c *appendEventChat) handleCommand(ctx context.Context, userInput string) (bool, bool, error) {
	lowerInput := strings.ToLower(userInput)

	switch {
	case lowerInput == "/exit":
		fmt.Println("👋 Goodbye!")
		return true, true, nil

	case strings.HasPrefix(lowerInput, "/append-system"):
		return c.handleAppendSystem(ctx, userInput)

	case strings.HasPrefix(lowerInput, "/append-assistant"):
		return c.handleAppendAssistant(ctx, userInput)

	case strings.HasPrefix(lowerInput, "/append"):
		return c.handleAppendUser(ctx, userInput)

	case lowerInput == "/events":
		c.listEvents(ctx)
		return true, false, nil

	case lowerInput == "/new":
		c.startNewSession()
		return true, false, nil

	case lowerInput == "/sessions":
		c.listSessions()
		return true, false, nil

	case strings.HasPrefix(lowerInput, "/use"):
		return c.handleUseSession(userInput)
	}

	return false, false, nil
}

// handleAppendSystem handles /append-system command.
func (c *appendEventChat) handleAppendSystem(ctx context.Context, userInput string) (bool, bool, error) {
	msg := strings.TrimSpace(userInput[len("/append-system"):])
	if msg == "" {
		fmt.Println("⚠️  Usage: /append-system <message>")
		return true, false, nil
	}
	return true, false, c.appendSystemMessage(ctx, msg)
}

// handleAppendAssistant handles /append-assistant command.
func (c *appendEventChat) handleAppendAssistant(ctx context.Context, userInput string) (bool, bool, error) {
	msg := strings.TrimSpace(userInput[len("/append-assistant"):])
	if msg == "" {
		fmt.Println("⚠️  Usage: /append-assistant <message>")
		return true, false, nil
	}
	return true, false, c.appendAssistantMessage(ctx, msg)
}

// handleAppendUser handles /append command.
func (c *appendEventChat) handleAppendUser(ctx context.Context, userInput string) (bool, bool, error) {
	msg := strings.TrimSpace(userInput[len("/append"):])
	if msg == "" {
		fmt.Println("⚠️  Usage: /append <message>")
		return true, false, nil
	}
	return true, false, c.appendUserMessage(ctx, msg)
}

// handleUseSession handles /use command.
func (c *appendEventChat) handleUseSession(userInput string) (bool, bool, error) {
	target := strings.TrimSpace(userInput[4:])
	if target == "" {
		fmt.Println("⚠️  Usage: /use <session-id>")
		return true, false, nil
	}
	c.switchSession(target)
	return true, false, nil
}

// appendUserMessage appends a user message directly to session.
func (c *appendEventChat) appendUserMessage(ctx context.Context, content string) error {
	message := model.NewUserMessage(content)
	return c.appendMessageToSession(ctx, message, "user")
}

// appendSystemMessage appends a system message directly to session.
func (c *appendEventChat) appendSystemMessage(ctx context.Context, content string) error {
	message := model.Message{
		Role:    model.RoleSystem,
		Content: content,
	}
	return c.appendMessageToSession(ctx, message, "system")
}

// appendAssistantMessage appends an assistant message directly to session.
func (c *appendEventChat) appendAssistantMessage(ctx context.Context, content string) error {
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}
	return c.appendMessageToSession(ctx, message, "assistant")
}

// appendMessageToSession appends a message as an event to the session.
//
// Note: An Event can represent both user requests and model responses.
// - User messages: Created when users send messages (author: "user")
// - Assistant messages: Created when model generates responses (author: agent name)
// - System messages: Created for system instructions (author: "system")
//
// Required fields for creating an event:
//   - invocationID: Unique identifier for this invocation (required)
//   - author: Event author, e.g., "user", "system", or agent name (required)
//   - response: *model.Response with at least Choices containing Message (required)
//
// Auto-generated fields (by event.NewResponseEvent):
//   - ID: Auto-generated UUID
//   - Timestamp: Auto-set to current time
//   - Version: Auto-set to CurrentVersion
//
// For persistence to session, Response must satisfy:
//   - Response != nil
//   - !IsPartial (or has StateDelta)
//   - IsValidContent() returns true (Choices with Message.Content, Message.ContentParts, or tool calls).
//
// Optional but recommended fields:
//   - RequestID: For request tracking (set manually)
//   - FilterKey: For event filtering (auto-set by framework)
func (c *appendEventChat) appendMessageToSession(
	ctx context.Context,
	message model.Message,
	author string,
) error {
	// Get or create session.
	sessionKey := session.Key{
		AppName:   "append-event-demo",
		UserID:    c.userID,
		SessionID: c.sessionID,
	}
	sess, err := c.sessionSvc.GetSession(ctx, sessionKey)
	if err != nil {
		return fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		sess, err = c.sessionSvc.CreateSession(ctx, sessionKey, session.StateMap{})
		if err != nil {
			return fmt.Errorf("create session failed: %w", err)
		}
	}

	// Create event from message.
	// Required: invocationID, author, response with Choices
	invocationID := uuid.New().String()
	evt := event.NewResponseEvent(
		invocationID, // Required: unique invocation identifier
		author,       // Required: event author
		&model.Response{
			// Required: Response with Choices
			Done: false, // Recommended: false for non-final events
			Choices: []model.Choice{
				{
					Index:   0,       // Required: choice index
					Message: message, // Required: message with Content or ContentParts
				},
			},
			// Optional Response fields:
			// Object:    "",  // Optional: object type
			// Created:   0,   // Optional: creation timestamp
			// Model:     "",  // Optional: model name
			// IsPartial: false, // Required: must be false for persistence
		},
	)
	// Optional: Set RequestID for tracking
	evt.RequestID = uuid.New().String()

	// Persist event to session.
	if err := c.sessionSvc.AppendEvent(ctx, sess, evt); err != nil {
		return fmt.Errorf("append event failed: %w", err)
	}

	fmt.Printf("✅ Message appended to session (author: %s)\n", author)
	return nil
}

// listEvents displays all events in the current session.
func (c *appendEventChat) listEvents(ctx context.Context) {
	sessionKey := session.Key{
		AppName:   "append-event-demo",
		UserID:    c.userID,
		SessionID: c.sessionID,
	}
	sess, err := c.sessionSvc.GetSession(ctx, sessionKey)
	if err != nil {
		fmt.Printf("❌ Get session failed: %v\n", err)
		return
	}

	fmt.Printf("\n📋 Session: %s\n", c.sessionID)
	fmt.Printf("   Total events: %d\n", sess.GetEventCount())
	if sess.GetEventCount() == 0 {
		fmt.Println("   (no events yet)")
		fmt.Println()
		return
	}

	sess.EventMu.RLock()
	for i, evt := range sess.Events {
		fmt.Printf("\n   Event %d:\n", i+1)
		fmt.Printf("     ID: %s\n", evt.ID)
		fmt.Printf("     Author: %s\n", evt.Author)
		fmt.Printf("     Timestamp: %s\n", evt.Timestamp.Format(time.RFC3339))
		if len(evt.Choices) > 0 {
			msg := evt.Choices[0].Message
			fmt.Printf("     Role: %s\n", msg.Role)
			if msg.Content != "" {
				content := msg.Content
				if len(content) > 100 {
					content = content[:100] + "..."
				}
				fmt.Printf("     Content: %s\n", content)
			}
			if len(msg.ContentParts) > 0 {
				fmt.Printf("     ContentParts: %d parts\n", len(msg.ContentParts))
			}
		}
	}
	sess.EventMu.RUnlock()
	fmt.Println()
}

// processMessage handles a single message exchange.
func (c *appendEventChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	requestID := uuid.New().String()
	// Run the agent through the runner.
	// The runner will automatically load all previously appended events
	// from the session and include them in the conversation context.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}
