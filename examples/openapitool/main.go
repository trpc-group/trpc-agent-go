//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the OpenAPI Tool.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/openapi"
)

var (
	modelName    = flag.String("model", "deepseek-chat", "Name of the model to use")
	modelBaseURL = flag.String("base_url", "", "Base URL for the model API")
	modelToken   = flag.String("api_token", "", "Authentication token for the model API")
	openAPISpec  = flag.String("openapi_spec", "./petstore3.yaml", "Path to the OpenAPI specification file")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Chat with LLMAgent\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &llmAgentChat{
		modelName:   *modelName,
		baseURL:     *modelBaseURL,
		Token:       *modelToken,
		openAPISpec: *openAPISpec,
	}
	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// llmAgentChat manages the conversation.
type llmAgentChat struct {
	modelName   string
	baseURL     string
	Token       string
	openAPISpec string

	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *llmAgentChat) run() error {
	ctx := context.Background()

	// Setup the agent.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the LLMAgent.
func (c *llmAgentChat) setup(_ context.Context) error {
	// Create a model instance.
	modelInstance := openai.New(c.modelName,
		openai.WithBaseURL(c.baseURL),
		openai.WithAPIKey(c.Token),
	)

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	loader, err := openapi.NewFileLoader(c.openAPISpec)
	if err != nil {
		return fmt.Errorf("failed to create openapi file loader: %w", err)
	}
	openAPIToolSet, err := openapi.NewToolSet(
		context.Background(),
		openapi.WithSpecLoader(loader),
	)
	if err != nil {
		return fmt.Errorf("failed to create openapi toolset: %w", err)
	}

	// Create an LLMAgent with configuration.
	llmAgent := llmagent.New(
		"chat-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant for interactive demonstrations"),
		llmagent.WithInstruction("You are a helpful AI assistant. Be conversational and engaging. "+
			"Answer questions clearly and provide helpful information."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithToolSets([]tool.ToolSet{openAPIToolSet}),
	)
	c.runner = runner.NewRunner("chat-assistant-app", llmAgent)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready!\n\n")

	return nil
}

// startChat runs the interactive conversation loop.
func (c *llmAgentChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Commands:")
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
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *llmAgentChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	// Run the agent.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run LLMAgent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}

// processResponse handles the streaming response.
func (c *llmAgentChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent strings.Builder

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
func (c *llmAgentChat) handleEvent(event *event.Event, fullContent *strings.Builder) error {
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
			fullContent.WriteString(content)
		}
	}

	return nil
}

// extractContent extracts content based on streaming mode.
func (c *llmAgentChat) extractContent(choice model.Choice) string {
	// In streaming mode, use delta content for real-time display.
	// In non-streaming mode, use full message content.
	return choice.Delta.Content
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 {
	return &f
}
