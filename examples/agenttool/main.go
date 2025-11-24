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
	fmt.Printf("Available tools: current_time, math-specialist(agent_tool)\n")
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

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

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
		llmagent.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request": map[string]any{
					"type":        "string",
					"description": "The mathematical problem or question to solve",
				},
			},
			"required": []any{"request"},
		}),
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
		if c.handleEvent(ev, &assistantStarted, &fullContent) {
			continue
		}
	}

	fmt.Println()
	return nil
}

// handleEvent processes a single event and returns true if the event was handled.
func (c *agentToolChat) handleEvent(ev *event.Event, assistantStarted *bool, fullContent *strings.Builder) bool {
	// Handle errors
	if ev.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
		return true
	}

	// Handle tool calls
	if c.handleToolCalls(ev, assistantStarted) {
		return true
	}

	// Handle inner agent streaming
	if c.handleInnerAgentStreaming(ev) {
		return true
	}

	// Handle outer assistant streaming
	if c.handleAssistantStreaming(ev, assistantStarted, fullContent) {
		return true
	}

	// Handle tool responses
	if c.handleToolResponses(ev) {
		return true
	}

	return false
}

// handleToolCalls processes tool call events.
func (c *agentToolChat) handleToolCalls(ev *event.Event, assistantStarted *bool) bool {
	if ev.Response == nil || len(ev.Response.Choices) == 0 {
		return false
	}

	ch := ev.Response.Choices[0]
	if len(ch.Message.ToolCalls) == 0 {
		return false
	}

	if *assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Tool calls initiated:\n")
	for _, tc := range ch.Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
		if len(tc.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(tc.Function.Arguments))
		}
	}
	fmt.Printf("\n🔄 Executing tools...\n")
	return true
}

// handleInnerAgentStreaming processes inner agent streaming events.
func (c *agentToolChat) handleInnerAgentStreaming(ev *event.Event) bool {
	if !c.showInner || ev.Author == c.agentName || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return false
	}

	ch := ev.Response.Choices[0]
	if ch.Delta.Content == "" {
		return false
	}

	if c.debugAuthors {
		fmt.Printf("[%s] ", ev.Author)
	}
	fmt.Print(ch.Delta.Content)
	return true
}

// handleAssistantStreaming processes outer assistant streaming events.
func (c *agentToolChat) handleAssistantStreaming(ev *event.Event, assistantStarted *bool, fullContent *strings.Builder) bool {
	if ev.Author != c.agentName || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return false
	}

	ch := ev.Response.Choices[0]
	if ch.Delta.Content == "" {
		return false
	}

	if c.debugAuthors && !*assistantStarted {
		fmt.Printf("[%s] ", ev.Author)
	}
	*assistantStarted = true
	fmt.Print(ch.Delta.Content)
	fullContent.WriteString(ch.Delta.Content)
	return true
}

// handleToolResponses processes tool response events.
func (c *agentToolChat) handleToolResponses(ev *event.Event) bool {
	if ev.Response == nil || ev.Object != model.ObjectTypeToolResponse || len(ev.Response.Choices) == 0 {
		return false
	}

	ch := ev.Response.Choices[0]
	if ch.Delta.Content != "" {
		// Partial tool delta - only show if not already shown via inner streaming
		if c.showTool && !c.showInner {
			fmt.Printf("\n🛠️  tool> %s", ch.Delta.Content)
		}
		return true
	}

	if ch.Message.Content != "" {
		// Final tool message - show detailed response
		if c.showTool {
			fmt.Printf("\n✅ Tool response (ID: %s): %s\n", ch.Message.ToolID, strings.TrimSpace(ch.Message.Content))
		} else {
			fmt.Printf("\n✅ Tool execution completed.\n")
		}
		return true
	}

	// Tool execution completed
	fmt.Printf("\n✅ Tool execution completed.\n")
	return true
}

// startNewSession creates a new session.
func (c *agentToolChat) startNewSession() {
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🔄 New session started: %s\n\n", c.sessionID)
}
