//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn chat using the Runner with streaming
// output, session management, and tool calling.
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/mysql"
	"trpc.group/trpc-go/trpc-agent-go/session/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName       = flag.String("model", "deepseek-chat", "Name of the model to use")
	sessServiceName = flag.String("session", "inmemory", "Name of the session service to use, inmemory / redis / pgsql / mysql")
	streaming       = flag.Bool("streaming", true, "Enable streaming mode for responses")
	enableParallel  = flag.Bool("enable-parallel", false, "Enable parallel tool execution (default: false, serial execution)")
	variant         = flag.String("variant", "openai", "Name of Variant to use when use openai provider, openai / hunyuan/ deepseek / qwen")
	enableThinking  = flag.Bool("enable-thinking", false, "Enable thinking mode for supported models (deepseek-reasoner, o1, etc.)")
)

// ANSI color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m" // For thinking content
	colorGreen  = "\033[32m" // For assistant response
	colorYellow = "\033[33m" // For timing info
)

// Environment variables for session services.
var (
	// Redis.
	redisAddr = getEnvOrDefault("REDIS_ADDR", "localhost:6379")

	// PostgreSQL.
	pgHost     = getEnvOrDefault("PG_HOST", "localhost")
	pgPort     = getEnvOrDefault("PG_PORT", "5432")
	pgUser     = getEnvOrDefault("PG_USER", "root")
	pgPassword = getEnvOrDefault("PG_PASSWORD", "")
	pgDatabase = getEnvOrDefault("PG_DATABASE", "trpc-agent-go")

	// MySQL.
	mysqlHost     = getEnvOrDefault("MYSQL_HOST", "localhost")
	mysqlPort     = getEnvOrDefault("MYSQL_PORT", "3306")
	mysqlUser     = getEnvOrDefault("MYSQL_USER", "root")
	mysqlPassword = getEnvOrDefault("MYSQL_PASSWORD", "")
	mysqlDatabase = getEnvOrDefault("MYSQL_DATABASE", "trpc_agent_go")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Multi-turn Chat with Runner + Tools\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	parallelStatus := "disabled (serial execution)"
	if *enableParallel {
		parallelStatus = "enabled (parallel execution)"
	}
	fmt.Printf("Parallel Tools: %s\n", parallelStatus)
	if *sessServiceName == "redis" {
		fmt.Printf("Redis: %s\n", redisAddr)
	} else if *sessServiceName == "pgsql" {
		fmt.Printf("PostgreSQL: %s:%s/%s\n", pgHost, pgPort, pgDatabase)
	} else if *sessServiceName == "mysql" {
		fmt.Printf("MySQL: %s:%s/%s\n", mysqlHost, mysqlPort, mysqlDatabase)
	}
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChat{
		modelName: *modelName,
		streaming: *streaming,
		variant:   *variant,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChat manages the conversation.
type multiTurnChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
	variant   string
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
func (c *multiTurnChat) setup(_ context.Context) error {
	// Create model with specified model name.
	modelInstance := openai.New(c.modelName, openai.WithVariant(openai.Variant(c.variant)))

	// Create session service based on configuration.
	var (
		err            error
		sessionService session.Service
	)
	switch *sessServiceName {
	case "inmemory":
		sessionService = sessioninmemory.NewSessionService()

	case "redis":
		redisURL := fmt.Sprintf("redis://%s", redisAddr)
		sessionService, err = redis.NewService(redis.WithRedisClientURL(redisURL))
		if err != nil {
			return fmt.Errorf("failed to create session service: %w", err)
		}

	case "pgsql":
		// Convert pgPort from string to int
		port := 5432
		if pgPort != "" {
			if p, parseErr := fmt.Sscanf(pgPort, "%d", &port); parseErr != nil || p != 1 {
				return fmt.Errorf("invalid PG_PORT value: %s", pgPort)
			}
		}
		sessionService, err = postgres.NewService(
			postgres.WithHost(pgHost),
			postgres.WithPort(port),
			postgres.WithUser(pgUser),
			postgres.WithPassword(pgPassword),
			postgres.WithDatabase(pgDatabase),
			postgres.WithTablePrefix("trpc_"),
		)
		if err != nil {
			return fmt.Errorf("failed to create postgres session service: %w", err)
		}

	case "mysql":
		// Build MySQL DSN
		mysqlDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
			mysqlUser, mysqlPassword, mysqlHost, mysqlPort, mysqlDatabase)
		sessionService, err = mysql.NewService(
			mysql.WithMySQLClientDSN(mysqlDSN),
			mysql.WithTablePrefix("trpc_"),
			mysql.WithSessionTTL(10*time.Second),
		)
		if err != nil {
			return fmt.Errorf("failed to create mysql session service: %w", err)
		}

	default:
		return fmt.Errorf("invalid session service name: %s", *sessServiceName)
	}

	// Create tools.
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"))

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	// Enable thinking mode if requested
	if *enableThinking {
		genConfig.ThinkingEnabled = boolPtr(true)
		genConfig.ThinkingTokens = intPtr(2048)
		fmt.Printf("🧠 Thinking mode enabled\n")
	}

	appName := "multi-turn-chat"
	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools."),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithEnableParallelTools(*enableParallel),
	)

	// Create runner.
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *multiTurnChat) startChat(ctx context.Context) error {
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

		fmt.Println() // Add spacing between turns.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	requestID := uuid.New().String()
	// Run the agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process response.
	return c.processResponse(eventChan)
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
	var (
		toolCallsDetected  bool
		assistantStarted   bool
		thinkingStarted    bool
		hasThinkingContent bool
	)

	for event := range eventChan {
		if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &thinkingStarted, &hasThinkingContent); err != nil {
			return err
		}

		// Check if this is the final event.
		// Do not break on tool response events (Done=true but not final assistant response).
		if event.IsFinalResponse() {
			// Print timing information at the end
			c.printTimingInfo(event)
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func (c *multiTurnChat) handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	thinkingStarted *bool,
	hasThinkingContent *bool,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls.
	if c.handleToolCalls(event, toolCallsDetected) {
		return nil
	}

	// Handle tool responses.
	if c.handleToolResponses(event) {
		return nil
	}

	// Handle content (both thinking and regular content).
	c.handleContent(event, toolCallsDetected, assistantStarted, thinkingStarted, hasThinkingContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func (c *multiTurnChat) handleToolCalls(event *event.Event, toolCallsDetected *bool) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		fmt.Printf("🔧 Tool calls initiated:\n")
		for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

// handleToolResponses detects and displays tool responses.
func (c *multiTurnChat) handleToolResponses(event *event.Event) bool {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		hasToolResponse := false
		for _, choice := range event.Response.Choices {
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("✅ Tool response (ID: %s): %s\n",
					choice.Message.ToolID,
					strings.TrimSpace(choice.Message.Content))
				hasToolResponse = true
			}
		}
		if hasToolResponse {
			return true
		}
	}
	return false
}

// handleContent processes and displays content (both thinking and regular content).
func (c *multiTurnChat) handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	thinkingStarted *bool,
	hasThinkingContent *bool,
) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]

		// Handle thinking content (reasoning) - extract from Delta or Message based on streaming mode
		thinkingContent := c.extractReasoningContent(choice)
		if thinkingContent != "" {
			*hasThinkingContent = true
			if !*thinkingStarted {
				fmt.Printf("%s🧠 Thinking:%s\n", colorCyan, colorReset)
				*thinkingStarted = true
			}
			fmt.Printf("%s%s%s", colorCyan, thinkingContent, colorReset)
		}

		// Handle regular content
		content := c.extractContent(choice)
		if content != "" {
			// If we were showing thinking content, add a separator
			if *thinkingStarted && !*assistantStarted {
				fmt.Printf("\n\n%s🤖 Response:%s\n", colorGreen, colorReset)
			} else if !*assistantStarted && !*thinkingStarted {
				fmt.Printf("%s🤖 Assistant:%s ", colorGreen, colorReset)
			}

			*assistantStarted = true
			fmt.Printf("%s%s%s", colorGreen, content, colorReset)
		}
	}
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

// extractReasoningContent extracts reasoning content based on streaming mode.
func (c *multiTurnChat) extractReasoningContent(choice model.Choice) string {
	if c.streaming {
		// Streaming mode: use delta reasoning content.
		return choice.Delta.ReasoningContent
	}
	// Non-streaming mode: use full message reasoning content.
	return choice.Message.ReasoningContent
}

// printTimingInfo displays timing information from the final event.
func (c *multiTurnChat) printTimingInfo(event *event.Event) {
	if event.Response == nil || event.Response.Usage == nil || event.Response.Usage.TimingInfo == nil {
		return
	}

	timing := event.Response.Usage.TimingInfo
	fmt.Printf("\n\n%s⏱️  Timing Info:%s\n", colorYellow, colorReset)

	// Time to first token (accumulated across all LLM calls in this flow)
	if timing.TimeToFirstToken > 0 {
		fmt.Printf("%s   • Time to first token: %v%s\n", colorYellow, timing.TimeToFirstToken, colorReset)
	}

	// Reasoning duration (accumulated across all LLM calls in this flow)
	if timing.ReasoningDuration > 0 {
		fmt.Printf("%s   • Reasoning: %v%s\n", colorYellow, timing.ReasoningDuration, colorReset)
	}

	// Token usage
	if event.Response.Usage.TotalTokens > 0 {
		fmt.Printf("   • Tokens: %d (prompt: %d, completion: %d)\n",
			event.Response.Usage.TotalTokens,
			event.Response.Usage.PromptTokens,
			event.Response.Usage.CompletionTokens)
	}
}

// startNewSession creates a new session ID.
func (c *multiTurnChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// getEnvOrDefault returns the environment variable value or a default value if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
