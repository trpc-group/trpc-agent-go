//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package main demonstrates how to use agent tools to wrap agents as tools
// within a larger application.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Agent Tool Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: current_time, agent_tool\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &agentToolChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// agentToolChat manages the conversation with agent tools.
type agentToolChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *agentToolChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and tools including agent tools.
func (c *agentToolChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create tools.
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)

	// Create a specialized agent for math operations.
	mathAgent := llmagent.New(
		"math-specialist",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized agent for mathematical operations and calculations"),
		llmagent.WithInstruction("You are a math specialist. Focus on mathematical operations, "+
			"calculations, and numerical reasoning. Provide clear, step-by-step solutions. "+
			"You have access to a calculator tool for basic operations."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1000),
			Temperature: floatPtr(0.3),
			Stream:      true,
		}),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)

	// Create tools.
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	// Create agent tool that wraps the math specialist agent.
	agentTool := agenttool.NewTool(
		mathAgent,
		agenttool.WithSkipSummarization(false),
	)

	// Create LLM agent with tools including the agent tool.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with time tools and agent tools"),
		llmagent.WithInstruction("Use tools when appropriate for time queries or "+
			"complex mathematical operations. For complex math problems, use the math-specialist agent tool. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{timeTool, agentTool}),
	)

	// Create runner.
	appName := "agent-tool-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *agentToolChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
	fmt.Println("   /exit      - End the conversation")
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

// processMessage handles a single message exchange.
func (c *agentToolChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response with tool call visualization.
func (c *agentToolChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 Tool calls initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Display tool results.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			// Display tool call results.
			if len(choice.Message.ToolCalls) > 0 {
				for _, toolCall := range choice.Message.ToolCalls {
					fmt.Printf("✅ Tool response (ID: %s): %s\n", toolCall.ID, toolCall.Function.Arguments)
				}
			}

			// Display assistant content.
			if choice.Message.Content != "" {
				if !assistantStarted {
					assistantStarted = true
				}
				fmt.Println(choice.Message.Content)
				fullContent += choice.Message.Content
			}
		}
	}

	if toolCallsDetected {
		fmt.Printf("\n\n✅ Tool execution completed.\n")
	}

	return nil
}

// startNewSession creates a new session.
func (c *agentToolChat) startNewSession() {
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🔄 New session started: %s\n\n", c.sessionID)
}

// calculate performs basic mathematical calculations.
func (c *agentToolChat) calculate(args calculatorArgs) calculatorResult {
	var result float64
	switch args.Operation {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return calculatorResult{
				Operation: args.Operation,
				A:         args.A,
				B:         args.B,
				Result:    0,
				Error:     "Division by zero",
			}
		}
		result = args.A / args.B
	default:
		return calculatorResult{
			Operation: args.Operation,
			A:         args.A,
			B:         args.B,
			Result:    0,
			Error:     "Unknown operation",
		}
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// getCurrentTime returns the current time for a specific timezone.
func (c *agentToolChat) getCurrentTime(args timeArgs) timeResult {
	loc := time.Local
	if args.Timezone != "" {
		switch strings.ToUpper(args.Timezone) {
		case "UTC":
			loc = time.UTC
		case "EST":
			loc = time.FixedZone("EST", -5*3600)
		case "PST":
			loc = time.FixedZone("PST", -8*3600)
		case "CST":
			loc = time.FixedZone("CST", -6*3600)
		}
	}

	now := time.Now().In(loc)
	return timeResult{
		Timezone: args.Timezone,
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Format("Monday"),
	}
}

// calculatorArgs defines the input arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// calculatorResult defines the output result for the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
	Error     string  `json:"error,omitempty"`
}

// timeArgs defines the input arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult defines the output result for the time tool.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions for creating pointers.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
