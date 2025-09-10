//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	modelName    = flag.String("model", "deepseek-chat", "Name of the model to use")
	debugAuthors = flag.Bool("debug", false, "Print event author names with streamed text")
	showTool     = flag.Bool("show-tool", false, "Show tool outputs (tool.response) in the transcript")
	showInner    = flag.Bool("show-inner", true, "Show inner agent transcript forwarded by agent tool")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Agent Tool Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Available tools: current_time, agent_tool\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &agentToolChat{
		modelName:    *modelName,
		debugAuthors: *debugAuthors,
		showTool:     *showTool,
		showInner:    *showInner,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// agentToolChat manages the conversation with agent tools.
type agentToolChat struct {
	modelName    string
	runner       runner.Runner
	userID       string
	sessionID    string
	debugAuthors bool
	agentName    string
	streaming    bool
	showTool     bool
	showInner    bool
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
func (c *agentToolChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

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
			"calculations, and numerical reasoning. When you receive a calculation request, "+
			"use your calculator tool to compute the result, then provide a clear, natural language response "+
			"explaining the calculation and result. Always explain what you calculated and present the answer clearly."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1000),
			Temperature: floatPtr(0.3),
			Stream:      true,
		}),
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
		agenttool.WithSkipSummarization(true),  // Skip summarization to get raw response
		agenttool.WithStreamInner(c.showInner), // Stream inner agent deltas when requested
	)

	// Create LLM agent with tools including the agent tool.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	c.agentName = "chat-assistant"
	llmAgent := llmagent.New(
		c.agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with time tools and agent tools"),
		llmagent.WithInstruction("Use tools when appropriate for time queries or "+
			"mathematical operations. For any math calculations, always use the math-specialist agent tool. "+
			"After receiving the math-specialist's response, present the result clearly to the user. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{timeTool, agentTool}),
	)

	// Remember streaming mode for printing logic.
	c.streaming = genConfig.Stream

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
		assistantStarted bool
		fullContent      strings.Builder
	)

	for ev := range eventChan {
		// Errors
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}

		// Show tool calls (assistant tool_calls)
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			ch := ev.Response.Choices[0]
			if len(ch.Message.ToolCalls) > 0 {
				if assistantStarted {
					fmt.Printf("\n")
				}
				if c.showTool {
					fmt.Printf("🔧 Tools: ")
					for i, tc := range ch.Message.ToolCalls {
						if i > 0 {
							fmt.Print(", ")
						}
						if len(tc.Function.Arguments) > 0 {
							fmt.Printf("%s(%s)", tc.Function.Name, string(tc.Function.Arguments))
						} else {
							fmt.Printf("%s", tc.Function.Name)
						}
					}
					fmt.Printf("\n")
				}
				continue
			}
		}

		// Forwarded inner agent deltas (from AgentTool streaming)
		if c.showInner && ev.Author != c.agentName && ev.Response != nil && len(ev.Response.Choices) > 0 {
			ch := ev.Response.Choices[0]
			if ch.Delta.Content != "" {
				if c.debugAuthors {
					fmt.Printf("[%s] ", ev.Author)
				}
				fmt.Print(ch.Delta.Content)
			}
			continue
		}

		// Outer assistant streaming
		if ev.Author == c.agentName && ev.Response != nil && len(ev.Response.Choices) > 0 {
			ch := ev.Response.Choices[0]
			if ch.Delta.Content != "" {
				if c.debugAuthors && !assistantStarted {
					fmt.Printf("[%s] ", ev.Author)
				}
				assistantStarted = true
				fmt.Print(ch.Delta.Content)
				fullContent.WriteString(ch.Delta.Content)
				continue
			}
		}

		// Tool response events
		if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse && len(ev.Response.Choices) > 0 {
			// The final (non-partial) tool.response includes the merged content for history.
			// To avoid duplication, don’t print aggregated content unless showTool is requested.
			if c.showTool {
				ch := ev.Response.Choices[0]
				if ch.Delta.Content != "" {
					// Partial tool delta
					fmt.Printf("\n🛠️  tool> %s", ch.Delta.Content)
				} else if ch.Message.Content != "" {
					fmt.Printf("\n🛠️  tool (final)> %s\n", ch.Message.Content)
				} else {
					fmt.Printf("\n🛠️  tool> (completed)\n")
				}
			} else {
				// Minimal marker when not showing tool details
				fmt.Printf("\n✅ Tool completed\n")
			}
			continue
		}
	}

	fmt.Println()
	return nil
}

// startNewSession creates a new session.
func (c *agentToolChat) startNewSession() {
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🔄 New session started: %s\n\n", c.sessionID)
}

// calculate performs basic mathematical calculations.
func (c *agentToolChat) calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
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
			}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	default:
		return calculatorResult{
			Operation: args.Operation,
			A:         args.A,
			B:         args.B,
			Result:    0,
			Error:     "Unknown operation",
		}, fmt.Errorf("unknown operation")
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// getCurrentTime returns the current time for a specific timezone.
func (c *agentToolChat) getCurrentTime(ctx context.Context, args timeArgs) (timeResult, error) {
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
	}, nil
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
