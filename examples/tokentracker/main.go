//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates token usage tracking for each conversation turn
// using the Runner with interactive command line interface.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Token Usage Tracker Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Special commands: /stats, /new, /exit\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &tokenTrackerChat{
		modelName: *modelName,
		streaming: *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// tokenTrackerChat manages the conversation with token usage tracking.
type tokenTrackerChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string

	// Token usage tracking
	sessionUsage *SessionTokenUsage
	turnCount    int
}

// SessionTokenUsage tracks token usage for the entire session.
type SessionTokenUsage struct {
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalTokens           int
	TurnCount             int
	UsageHistory          []TurnUsage
}

// TurnUsage represents token usage for a single turn.
type TurnUsage struct {
	TurnNumber        int
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	Model             string
	InvocationID      string
	Timestamp         time.Time
	UserMessage       string
	AssistantResponse string
}

// run starts the interactive chat session.
func (c *tokenTrackerChat) run() error {
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

// setup creates the runner with LLM agent and tools.
func (c *tokenTrackerChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create LLM agent without tools for simplicity.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	agentName := "token-tracker-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant that demonstrates token usage tracking"),
		llmagent.WithInstruction("Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
	appName := "token-tracker-demo"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers and token tracking.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("token-tracker-session-%d", time.Now().Unix())
	c.sessionUsage = &SessionTokenUsage{
		UsageHistory: make([]TurnUsage, 0),
	}

	fmt.Printf("✅ Token tracker ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *tokenTrackerChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /stats    - Show current session token usage statistics")
	fmt.Println("   /new      - Start a new session (reset token tracking)")
	fmt.Println("   /exit     - End the conversation")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands.
		switch strings.ToLower(userInput) {
		case "/exit":
			c.showFinalStats()
			fmt.Println("👋 Goodbye!")
			return nil
		case "/stats":
			c.showStats()
			continue
		case "/new":
			c.startNewSession()
			continue
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange with token tracking.
func (c *tokenTrackerChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	c.turnCount++

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response and track token usage.
	return c.processResponse(eventChan, userMessage)
}

// processResponse handles both streaming and non-streaming responses with token tracking.
func (c *tokenTrackerChat) processResponse(eventChan <-chan *event.Event, userMessage string) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent string
		turnUsage   *TurnUsage
	)

	for event := range eventChan {
		// Track token usage from the event.
		if event.Response != nil && event.Response.Usage != nil {
			if turnUsage == nil {
				turnUsage = &TurnUsage{
					TurnNumber:   c.turnCount,
					Model:        event.Response.Model,
					InvocationID: event.InvocationID,
					Timestamp:    event.Response.Timestamp,
					UserMessage:  userMessage,
				}
			}

			// Update token usage (use the latest values from the event).
			turnUsage.PromptTokens = event.Response.Usage.PromptTokens
			turnUsage.CompletionTokens = event.Response.Usage.CompletionTokens
			turnUsage.TotalTokens = event.Response.Usage.TotalTokens
		}

		// Handle content for display.
		if len(event.Response.Choices) > 0 {
			if event.Response.Choices[0].Delta.Content != "" {
				fmt.Print(event.Response.Choices[0].Delta.Content)
				fullContent += event.Response.Choices[0].Delta.Content
			} else if event.Response.Choices[0].Message.Content != "" {
				fmt.Print(event.Response.Choices[0].Message.Content)
				fullContent += event.Response.Choices[0].Message.Content
			}
		}

		// Check if this is the final event.
		if event.Done {
			if turnUsage != nil {
				turnUsage.AssistantResponse = fullContent
				c.addTurnUsage(*turnUsage)
			}

			// Show turn-specific token usage.
			if turnUsage != nil {
				fmt.Printf("\n📊 Turn %d Token Usage:\n", c.turnCount)
				fmt.Printf("   Prompt: %d, Completion: %d, Total: %d\n",
					turnUsage.PromptTokens,
					turnUsage.CompletionTokens,
					turnUsage.TotalTokens)
			}

			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// addTurnUsage adds token usage for a single turn to the session tracking.
func (c *tokenTrackerChat) addTurnUsage(usage TurnUsage) {
	c.sessionUsage.TotalPromptTokens += usage.PromptTokens
	c.sessionUsage.TotalCompletionTokens += usage.CompletionTokens
	c.sessionUsage.TotalTokens += usage.TotalTokens
	c.sessionUsage.TurnCount++
	c.sessionUsage.UsageHistory = append(c.sessionUsage.UsageHistory, usage)
}

// showStats displays current session token usage statistics.
func (c *tokenTrackerChat) showStats() {
	fmt.Printf("\n📊 Session Token Usage Statistics:\n")
	fmt.Printf("   Total Turns: %d\n", c.sessionUsage.TurnCount)
	fmt.Printf("   Total Prompt Tokens: %d\n", c.sessionUsage.TotalPromptTokens)
	fmt.Printf("   Total Completion Tokens: %d\n", c.sessionUsage.TotalCompletionTokens)
	fmt.Printf("   Total Tokens: %d\n", c.sessionUsage.TotalTokens)

	if c.sessionUsage.TurnCount > 0 {
		avgPrompt := float64(c.sessionUsage.TotalPromptTokens) / float64(c.sessionUsage.TurnCount)
		avgCompletion := float64(c.sessionUsage.TotalCompletionTokens) / float64(c.sessionUsage.TurnCount)
		avgTotal := float64(c.sessionUsage.TotalTokens) / float64(c.sessionUsage.TurnCount)

		fmt.Printf("   Average Prompt Tokens per Turn: %.1f\n", avgPrompt)
		fmt.Printf("   Average Completion Tokens per Turn: %.1f\n", avgCompletion)
		fmt.Printf("   Average Total Tokens per Turn: %.1f\n", avgTotal)
	}
	fmt.Println()
}

// showFinalStats displays final statistics when exiting.
func (c *tokenTrackerChat) showFinalStats() {
	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("🎯 Final Session Statistics:\n")
	c.showStats()
}

// startNewSession creates a new session and resets token tracking.
func (c *tokenTrackerChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("token-tracker-session-%d", time.Now().Unix())
	c.sessionUsage = &SessionTokenUsage{
		UsageHistory: make([]TurnUsage, 0),
	}
	c.turnCount = 0

	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   Token tracking has been reset.\n")
	fmt.Println()
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
