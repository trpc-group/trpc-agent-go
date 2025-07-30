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

// Package main demonstrates multi-turn chat using the Runner with streaming output, session management,
// tool calling, and shows how to use AgentCallbacks, ModelCallbacks, and ToolCallbacks.
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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName       = flag.String("model", "deepseek-chat", "Name of the model to use")
	redisAddr       = flag.String("redis-addr", "localhost:6379", "Redis address")
	sessServiceName = flag.String("session", "inmemory", "Name of the session service to use, inmemory / redis")
	streaming       = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Multi-turn Chat with Runner + Tools + Callbacks\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChatWithCallbacks{
		modelName: *modelName,
		streaming: *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChatWithCallbacks manages the chat with callbacks.
type multiTurnChatWithCallbacks struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the interactive chat session.
func (c *multiTurnChatWithCallbacks) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and tools.
func (c *multiTurnChatWithCallbacks) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.WithChannelBufferSize(512))

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

	// Construct ModelCallbacks example.
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(
		ctx context.Context, req *model.Request,
	) (*model.Response, error) {
		userMsg := ""
		if len(req.Messages) > 0 {
			userMsg = req.Messages[len(req.Messages)-1].Content
		}
		fmt.Printf("\n🔵 BeforeModelCallback: model=%s, lastUserMsg=%q\n",
			c.modelName,
			userMsg,
		)
		if userMsg != "" && strings.Contains(userMsg, "custom model") {
			fmt.Printf("🔵 BeforeModelCallback: triggered, returning custom response for 'custom model'.\n")
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "[This is a custom response from before model callback]",
					},
				}},
			}, nil
		}
		return nil, nil
	})
	modelCallbacks.RegisterAfterModel(func(
		ctx context.Context, resp *model.Response, runErr error,
	) (*model.Response, error) {
		if resp != nil && resp.Done {
			fmt.Printf("\n🟣 AfterModelCallback: model=%s has finished\n", c.modelName)
		}
		if resp != nil && len(resp.Choices) > 0 && strings.Contains(resp.Choices[0].Message.Content, "override me") {
			fmt.Printf("🟣 AfterModelCallback: triggered, overriding response for 'override me'.\n")
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "[This response was overridden by after model callback]",
					},
				}},
			}, nil
		}
		return nil, nil
	})

	// Construct ToolCallbacks example.
	toolCallbacks := tool.NewCallbacks()
	toolCallbacks.RegisterBeforeTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=%s\n", toolName, string(jsonArgs))
		if toolName == "calculator" && strings.Contains(string(jsonArgs), "42") {
			fmt.Println("\n🟠 BeforeToolCallback: triggered, custom result returned for calculator with 42.")
			return calculatorResult{
				Operation: "custom",
				A:         42,
				B:         42,
				Result:    4242,
			}, nil
		}
		return nil, nil
	})
	toolCallbacks.RegisterAfterTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
		result any,
		runErr error,
	) (any, error) {
		fmt.Printf("\n🟤 AfterToolCallback: tool=%s, args=%s, result=%v, err=%v\n", toolName, string(jsonArgs), result, runErr)
		if toolName == "current_time" {
			if timeResult, ok := result.(timeResult); ok {
				timeResult.Formatted = fmt.Sprintf("%s %s (%s)", timeResult.Date, timeResult.Time, timeResult.Timezone)
				fmt.Println("\n🟤 AfterToolCallback: triggered, formatted result.")
				return timeResult, nil
			}
		}
		return nil, nil
	})

	// AgentCallbacks example.
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(
		ctx context.Context, invocation *agent.Invocation,
	) (*model.Response, error) {
		fmt.Printf("\n🟢 BeforeAgentCallback: agent=%s, invocationID=%s, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			invocation.Message.Content,
		)
		return nil, nil
	})
	agentCallbacks.RegisterAfterAgent(func(
		ctx context.Context, invocation *agent.Invocation, runErr error,
	) (*model.Response, error) {
		respContent := "<nil>"
		if invocation != nil && invocation.Message.Content != "" {
			respContent = invocation.Message.Content
		}
		fmt.Printf("\n🟡 AfterAgentCallback: agent=%s, invocationID=%s, runErr=%v, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			runErr,
			respContent,
		)
		return nil, nil
	})

	// Create LLM agent with tools and callbacks.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithToolCallbacks(toolCallbacks),
	)

	var sessionService session.Service
	var err error
	switch *sessServiceName {
	case "inmemory":
		sessionService = inmemory.NewSessionService()
	case "redis":
		redisURL := fmt.Sprintf("redis://%s", *redisAddr)
		sessionService, err = redis.NewService(redis.WithRedisClientURL(redisURL))
	default:
		return fmt.Errorf("invalid session service name: %s", *sessServiceName)
	}

	if err != nil {
		return fmt.Errorf("failed to create session service: %w", err)
	}

	// Create runner.
	appName := "multi-turn-chat-callbacks"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat with callbacks ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChatWithCallbacks) startChat(ctx context.Context) error {
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
func (c *multiTurnChatWithCallbacks) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func (c *multiTurnChatWithCallbacks) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		// Don't break on tool response events (Done=true but not final assistant response).
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func (c *multiTurnChatWithCallbacks) handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls.
	if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses.
	if c.handleToolResponses(event) {
		return nil
	}

	// Handle content.
	c.handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls processes tool call events and returns true if handled.
func (c *multiTurnChatWithCallbacks) handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Choices) == 0 || len(event.Choices[0].Message.ToolCalls) == 0 {
		return false
	}

	*toolCallsDetected = true
	if *assistantStarted {
		fmt.Printf("\n")
	}

	fmt.Printf("🔧 CallableTool calls initiated:\n")
	for _, toolCall := range event.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
		}
	}
	fmt.Printf("\n🔄 Executing tools...\n")

	return true
}

// handleToolResponses processes tool response events and returns true if handled.
func (c *multiTurnChatWithCallbacks) handleToolResponses(event *event.Event) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}

	return hasToolResponse
}

// handleContent processes content events for both streaming and non-streaming modes.
func (c *multiTurnChatWithCallbacks) handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Choices) == 0 {
		return
	}

	choice := event.Choices[0]
	content := c.extractContent(choice)

	if content == "" {
		return
	}

	c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
}

// extractContent extracts content based on streaming mode.
func (c *multiTurnChatWithCallbacks) extractContent(choice model.Choice) string {
	if c.streaming {
		// Streaming mode: use delta content.
		return choice.Delta.Content
	}
	// Non-streaming mode: use full message content.
	return choice.Message.Content
}

// displayContent displays the content with proper formatting.
func (c *multiTurnChatWithCallbacks) displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*assistantStarted = true
	}

	fmt.Print(content)
	*fullContent += content
}

// isToolEvent checks if an event is a tool response (not a final response).
func (c *multiTurnChatWithCallbacks) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices.
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

// startNewSession creates a new session ID.
func (c *multiTurnChatWithCallbacks) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// CallableTool implementations.

// calculate performs basic mathematical operations.
func (c *multiTurnChatWithCallbacks) calculate(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
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
			result = 0 // Handle division by zero.
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
func (c *multiTurnChatWithCallbacks) getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST.
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST.
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST.
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
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
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
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone  string `json:"timezone"`
	Time      string `json:"time"`
	Date      string `json:"date"`
	Weekday   string `json:"weekday"`
	Formatted string `json:"formatted,omitempty"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
