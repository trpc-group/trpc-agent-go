//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn chat using the Runner with streaming
// output and time awareness.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName  = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming  = flag.Bool("streaming", true, "Enable streaming mode for responses")
	addTime    = flag.Bool("add-time", true, "Add current time to the system prompt")
	timezone   = flag.String("timezone", "UTC", "Timezone for time display (e.g., UTC, EST, PST)")
	timeFormat = flag.String("time-format", "2006-01-02 15:04:05 UTC", "Time format for display (Go time format)")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Multi-turn Chat with Time Aware\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Add Current Time: %t\n", *addTime)
	fmt.Printf("Timezone: %s\n", *timezone)
	fmt.Printf("Time Format: %s\n", *timeFormat)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChat{
		modelName:  *modelName,
		streaming:  *streaming,
		addTime:    *addTime,
		timezone:   *timezone,
		timeFormat: *timeFormat,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation.
type multiTurnChat struct {
	modelName  string
	streaming  bool
	runner     runner.Runner
	userID     string
	addTime    bool
	timezone   string
	timeFormat string
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

// setup creates the runner with LLM agent.
func (c *multiTurnChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create LLM agent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with time awareness"),
		llmagent.WithInstruction("Be helpful and conversational. "+
			"You have access to current time information."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithAddCurrentTime(c.addTime),
		llmagent.WithTimezone(c.timezone),
		llmagent.WithTimeFormat(c.timeFormat),
	)

	// Create runner.
	appName := "multi-turn-chat"
	c.runner = runner.NewRunner(appName, llmAgent)

	// Setup identifiers.
	c.userID = "user"

	fmt.Printf("✅ Chat ready!\n\n")

	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Type 'exit' to end the conversation")
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

		// Handle exit command.
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
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

// processMessage handles a single message exchange.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, "chat", message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses.
func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent string

	for event := range eventChan {
		if err := c.handleEvent(event, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		if event.Done {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func (c *multiTurnChat) handleEvent(event *event.Event, fullContent *string) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle content.
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		content := c.extractContent(choice)

		if content != "" {
			fmt.Print(content)
			*fullContent += content
		}
	}

	return nil
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

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
