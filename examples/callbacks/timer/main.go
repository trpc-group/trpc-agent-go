//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates tool execution timing using ToolCallbacks with OpenTelemetry integration.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"go.opentelemetry.io/otel/metric"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", false, "Enable streaming mode for responses")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Println("🚀 Tool Timer with Telemetry Example")
	fmt.Println("This example demonstrates how to use ToolCallbacks to measure tool execution time and report to OpenTelemetry.")
	fmt.Println(strings.Repeat("=", 70))

	// Initialize OpenTelemetry.
	meter, err := initTelemetry()
	if err != nil {
		log.Fatalf("Failed to initialize telemetry: %v", err)
	}

	// Create the example.
	example := &toolTimerExample{meter: meter}

	// Setup and run.
	if err := example.run(); err != nil {
		log.Fatalf("Example failed: %v", err)
	}
}

// toolTimerExample demonstrates tool execution timing with telemetry integration.
type toolTimerExample struct {
	meter     metric.Meter
	runner    runner.Runner
	userID    string
	sessionID string
	// Add telemetry metrics.
	agentDurationHistogram metric.Float64Histogram
	toolDurationHistogram  metric.Float64Histogram
	modelDurationHistogram metric.Float64Histogram
	agentCounter           metric.Int64Counter
	toolCounter            metric.Int64Counter
	modelCounter           metric.Int64Counter
}

// run executes the tool timer example.
func (e *toolTimerExample) run() error {
	ctx := context.Background()

	// Initialize telemetry metrics.
	if err := e.initMetrics(); err != nil {
		return fmt.Errorf("failed to initialize metrics: %w", err)
	}

	// Setup the runner.
	if err := e.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer e.runner.Close()

	// Run the example.
	return e.runExample(ctx)
}

// setup creates the runner with LLM agent, tools and telemetry metrics.
func (e *toolTimerExample) setup(_ context.Context) error {
	// Create OpenAI model using flag.
	modelInstance := openai.New(*modelName)

	// Create tools.
	tools := e.createTools()

	// Create callbacks for timing.
	agentCallbacks := e.createAgentCallbacks()
	modelCallbacks := e.createModelCallbacks()
	toolCallbacks := e.createToolCallbacks()

	// Create LLM agent with tools and callbacks.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}
	llmAgent := llmagent.New(
		"tool-timer-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant that demonstrates tool execution timing"),
		llmagent.WithInstruction("Use the calculator tool when asked to perform calculations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithToolCallbacks(toolCallbacks),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	// Create runner.
	e.runner = runner.NewRunner(
		"tool-timer-example",
		llmAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)

	// Setup identifiers.
	e.userID = "user"
	e.sessionID = fmt.Sprintf("tool-timer-session-%d", time.Now().Unix())
	fmt.Printf("✅ Tool timer example ready! Session: %s\n\n", e.sessionID)

	return nil
}

// createTools creates the tools for the agent.
func (e *toolTimerExample) createTools() []tool.Tool {
	calculatorTool := function.NewFunctionTool(
		e.calculator,
		function.WithName("calculator"),
		function.WithDescription("Perform basic calculations"),
	)
	return []tool.Tool{calculatorTool}
}

// runExample executes the interactive chat session for tool timer example.
func (e *toolTimerExample) runExample(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Tool Timer Example - Interactive Chat")
	fmt.Println("Available tools: calculator")
	fmt.Println("Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
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
		case "/history":
			userInput = "show our conversation history"
		case "/new":
			e.startNewSession()
			continue
		}

		// Process the user message.
		if err := e.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			fmt.Println() // Add spacing after error.
		}
		// Wait briefly for AfterAgentCallback to complete its output.
		// This ensures timing information appears before the next prompt.
		time.Sleep(50 * time.Millisecond)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (e *toolTimerExample) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := e.runner.Run(ctx, e.userID, e.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return e.processResponse(eventChan)
}

// startNewSession creates a new session ID.
func (e *toolTimerExample) startNewSession() {
	oldSessionID := e.sessionID
	e.sessionID = fmt.Sprintf("tool-timer-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", e.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// processResponse handles the response from the agent.
func (e *toolTimerExample) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			return nil
		}

		// Handle tool calls.
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			fmt.Printf("\n🔧 Tool calls:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Handle tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool response (ID: %s): %s\n",
						choice.Message.ToolID,
						choice.Message.Content)
				}
			}
		}

		// Handle content.
		if len(event.Response.Choices) > 0 && event.Response.Choices[0].Message.Content != "" {
			fmt.Print(event.Response.Choices[0].Message.Content)
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}
