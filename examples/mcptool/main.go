//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates streamable http and stdio mcp tools usage.
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
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🚀 MCP tools usage (STDIO, Streamable HTTP, and SSE)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time, echo, add, get_weather, get_news, sse_echo, sse_info\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation.
type multiTurnChat struct {
	modelName  string
	runner     runner.Runner
	userID     string
	sessionID  string
	mcpToolSet []*mcp.ToolSet
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

// setup creates the runner with LLM agent and tools.
func (c *multiTurnChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create tools.
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	// Create Stdio MCP tools.
	stdioToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "stdio",
			Command:   "go",
			Args:      []string{"run", "./stdioserver/main.go"},
			Timeout:   10 * time.Second,
		},
		mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("echo", "add")),
	)
	if err := stdioToolSet.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize STDIO MCP toolset: %w", err)
	}
	fmt.Println("STDIO MCP Toolset initialized successfully")

	// Create Streamable MCP tools.
	streamableToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: "http://localhost:3000/mcp", // Use ServerURL instead of URL
			Timeout:   10 * time.Second,
		},
		mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("get_weather", "get_news")),
		mcp.WithMCPOptions(
			// WithSimpleRetry(3): Uses default settings with 3 retry attempts
			// - MaxRetries: 3 (range: 0-10)
			// - InitialBackoff: 500ms (default, range: 1ms-30s)
			// - BackoffFactor: 2.0 (default, range: 1.0-10.0)
			// - MaxBackoff: 8s (default, range: up to 5 minutes)
			// Retry sequence: 500ms -> 1s -> 2s (total max delay: ~3.5s)
			tmcp.WithSimpleRetry(3),
			// other mcp options.
			// tmcp.WithHTTPHeaders(http.Header{
			// 	"User-Agent": []string{"trpc-agent-go/1.0.0"},
			// }),
		),
	)
	if err := streamableToolSet.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize Streamable MCP toolset: %w", err)
	}
	fmt.Println("Streamable MCP Toolset initialized successfully")

	// Create SSE MCP tools with session reconnection.
	sseToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "sse",
			ServerURL: "http://localhost:8080/sse", // SSE server URL.
			Timeout:   10 * time.Second,
			Headers: map[string]string{
				"User-Agent": "trpc-agent-go/1.0.0",
			},
		},
		mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("sse_recipe", "sse_health_tip")),
		// Enable session reconnection for automatic recovery when server restarts (max 3 attempts)
		mcp.WithSessionReconnect(3),
		mcp.WithMCPOptions(
			// WithRetry: Custom retry configuration for fine-tuned control.
			// Retry sequence: 1s -> 1.5s -> 2.25s -> 3.375s -> 5.0625s (capped at 15s)
			tmcp.WithRetry(tmcp.RetryConfig{
				MaxRetries:     5,                // Maximum retry attempts (range: 0-10, default: 2)
				InitialBackoff: 1 * time.Second,  // Initial delay before first retry (range: 1ms-30s, default: 500ms)
				BackoffFactor:  1.5,              // Exponential backoff multiplier (range: 1.0-10.0, default: 2.0)
				MaxBackoff:     15 * time.Second, // Maximum delay cap (range: up to 5 minutes, default: 8s)
			}),
		),
	)
	if err := sseToolSet.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize SSE MCP toolset: %w", err)
	}

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithToolSets([]tool.ToolSet{stdioToolSet, streamableToolSet, sseToolSet}),
	)

	// Create runner.
	appName := "multi-turn-chat"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	// Store toolsets for proper cleanup.
	c.mcpToolSet = []*mcp.ToolSet{stdioToolSet, streamableToolSet, sseToolSet}

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChat) startChat(ctx context.Context) error {
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

	// Close the toolset when done.
	for _, toolSet := range c.mcpToolSet {
		toolSet.Close()
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
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
func (c *multiTurnChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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
		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 CallableTool calls initiated:\n")
			for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools (with session reconnection if needed)...\n")
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool response received (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Check if this is the final event.
		// Don't break on tool response events (Done=true but not final assistant response).
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// CallableTool implementations.

// calculate performs basic mathematical operations.
func (c *multiTurnChat) calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		} else {
			result = 0 // Handle division by zero
		}
	default:
		result = 0
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// getCurrentTime returns current time information.
func (c *multiTurnChat) getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}

	return timeResult{
		Timezone: timezone,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}, nil
}

// calculatorArgs represents arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation to perform"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// calculatorResult represents the result of a calculation.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// timeArgs represents arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
