//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates output schema functionality using LLMAgent
// with structured output validation. This example shows how to constrain
// agent responses to specific JSON schemas for consistent data formats.
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
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	maxTokens   = 800
	temperature = 0.3
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("📋 Output Schema Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()
	fmt.Println("💡 Example queries to try:")
	fmt.Println("   • What's the weather like in Beijing today?")
	fmt.Println("   • Tell me about the weather in Shanghai")
	fmt.Println("   • How's the weather in Guangzhou?")
	fmt.Println("   • Weather forecast for Shenzhen")
	fmt.Println("   • What's the climate like in Chengdu?")
	fmt.Println()
	fmt.Println("🔄 How it works:")
	fmt.Println("   1. Agent analyzes your weather query")
	fmt.Println("   2. Returns structured JSON with temperature, conditions, etc.")
	fmt.Println("   3. Output is validated against a predefined schema")
	fmt.Println()

	// Create and run the chat.
	chat := &outputSchemaChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Output schema chat failed: %v", err)
	}
}

// outputSchemaChat manages the output schema conversation.
type outputSchemaChat struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

// run starts the interactive chat session.
func (c *outputSchemaChat) run() error {
	ctx := context.Background()

	// Setup the runner with output schema agent.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with output schema agent.
func (c *outputSchemaChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create session service.
	sessionService := inmemory.NewSessionService()
	c.sessionService = sessionService

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      true,
	}

	// Define output schema for weather information.
	weatherSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "The city name",
			},
			"temperature": map[string]any{
				"type":        "number",
				"description": "Temperature in Celsius",
			},
			"condition": map[string]any{
				"type":        "string",
				"description": "Weather condition (sunny, cloudy, rainy, etc.)",
				"enum":        []string{"sunny", "cloudy", "rainy", "snowy", "foggy", "windy"},
			},
			"humidity": map[string]any{
				"type":        "number",
				"description": "Humidity percentage (0-100)",
			},
			"wind_speed": map[string]any{
				"type":        "number",
				"description": "Wind speed in km/h",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable weather description",
			},
			"recommendations": map[string]any{
				"type":        "array",
				"description": "List of recommendations based on weather",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"city", "temperature", "condition", "description"},
	}

	// Create Weather Agent with output schema.
	weatherAgent := llmagent.New(
		"weather-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A weather information agent that provides structured weather data"),
		llmagent.WithInstruction("You are a weather information specialist. When users ask about weather, "+
			"analyze their query and provide comprehensive weather information in a structured format. "+
			"Extract the city name from their query and provide realistic weather data including temperature, "+
			"conditions, humidity, wind speed, and helpful recommendations. Always respond with valid JSON "+
			"that matches the required schema."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithOutputSchema(weatherSchema),
	)

	// Create runner with the weather agent and session service.
	appName := "output-schema-demo"
	c.runner = runner.NewRunner(
		appName,
		weatherAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("output-schema-session-%d", time.Now().Unix())

	fmt.Printf("✅ Output Schema Agent ready! Session: %s\n", c.sessionID)
	fmt.Printf("📋 Schema: Structured weather data with validation\n\n")

	return nil
}

// startChat runs the interactive conversation loop.
func (c *outputSchemaChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

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

// processMessage handles a single message exchange through the agent.
func (c *outputSchemaChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the weather agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run weather agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the agent.
func (c *outputSchemaChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Printf("🌤️  Weather Agent: ")

	for event := range eventChan {
		if err := c.handleEvent(event); err != nil {
			return err
		}

		// Check if this is the final runner completion event.
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// handleEvent processes a single event from the agent.
func (c *outputSchemaChat) handleEvent(event *event.Event) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle streaming content.
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		}
	}

	return nil
}

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
