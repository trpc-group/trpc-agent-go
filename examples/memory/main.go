//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates memory management using the Runner with streaming
// output, session management, and memory tools.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName      = flag.String("model", "deepseek-chat", "Name of the model to use")
	memServiceName = flag.String("memory", "inmemory", "Name of the memory service to use, inmemory / redis / mysql / postgres")
	redisAddr      = flag.String("redis-addr", "localhost:6379", "Redis address")
	dsn            = flag.String("dsn", "", "Database DSN (MySQL: user:password@tcp(localhost:3306)/dbname?parseTime=true, "+
		"PostgreSQL: postgres://user:password@localhost:5432/dbname)")
	softDelete = flag.Bool("soft-delete", false, "Enable soft delete for MySQL/PostgreSQL memory service")
	streaming  = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧠 Multi Turn Chat with Memory\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Memory Service: %s\n", *memServiceName)
	if *memServiceName == "redis" {
		fmt.Printf("Redis: %s\n", *redisAddr)
	}
	if *memServiceName == "mysql" {
		fmt.Printf("MySQL: %s\n", *dsn)
		fmt.Printf("Soft delete: %t\n", *softDelete)
	}
	if *memServiceName == "postgres" {
		fmt.Printf("PostgreSQL: %s\n", *dsn)
		fmt.Printf("Soft delete: %t\n", *softDelete)
	}
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Available tools: memory_add, memory_update, memory_search, memory_load\n")
	fmt.Printf("(memory_delete, memory_clear disabled by default, and can be enabled or customized)\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &memoryChat{
		modelName:      *modelName,
		memServiceName: *memServiceName,
		redisAddr:      *redisAddr,
		dsn:            *dsn,
		streaming:      *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// memoryChat manages the conversation with memory capabilities.
type memoryChat struct {
	modelName      string
	memServiceName string
	redisAddr      string
	dsn            string
	streaming      bool
	runner         runner.Runner
	userID         string
	sessionID      string
}

// run starts the interactive chat session.
func (c *memoryChat) run() error {
	ctx := context.Background()

	// Setup the runner.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with LLM agent and memory tools.
func (c *memoryChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create memory service based on configuration.
	var (
		memoryService memory.Service
		err           error
	)

	switch c.memServiceName {
	case "redis":
		redisURL := fmt.Sprintf("redis://%s", c.redisAddr)
		memoryService, err = memoryredis.NewService(
			memoryredis.WithRedisClientURL(redisURL),
			// You can enable or disable tools and create custom tools here.
			memoryredis.WithToolEnabled(memory.DeleteToolName, false),               // delete tool is disabled by default
			memoryredis.WithCustomTool(memory.ClearToolName, customClearMemoryTool), // custom clear tool
		)
		if err != nil {
			return fmt.Errorf("failed to create redis memory service: %w", err)
		}
	case "mysql":
		if c.dsn == "" {
			return errors.New("DSN is required for MySQL, set via --dsn flag")
		}
		memoryService, err = memorymysql.NewService(
			memorymysql.WithMySQLClientDSN(c.dsn),
			memorymysql.WithSoftDelete(*softDelete),
			// You can enable or disable tools and create custom tools here.
			memorymysql.WithToolEnabled(memory.DeleteToolName, false),               // delete tool is disabled by default
			memorymysql.WithCustomTool(memory.ClearToolName, customClearMemoryTool), // custom clear tool
		)
		if err != nil {
			return fmt.Errorf("failed to create mysql memory service: %w", err)
		}
	case "postgres":
		if c.dsn == "" {
			return errors.New("DSN is required for PostgreSQL, set via --dsn flag")
		}
		memoryService, err = memorypostgres.NewService(
			memorypostgres.WithPostgresConnString(c.dsn),
			memorypostgres.WithSoftDelete(*softDelete),
			// You can enable or disable tools and create custom tools here.
			memorypostgres.WithToolEnabled(memory.DeleteToolName, false),               // delete tool is disabled by default
			memorypostgres.WithCustomTool(memory.ClearToolName, customClearMemoryTool), // custom clear tool
		)
		if err != nil {
			return fmt.Errorf("failed to create postgres memory service: %w", err)
		}
	default: // inmemory
		memoryService = memoryinmemory.NewMemoryService(
			// You can enable or disable tools and create custom tools here.
			memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),                // delete tool is disabled by default
			memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool), // custom clear tool
		)
	}

	// Setup identifiers first.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("memory-session-%d", time.Now().Unix())

	// Create LLM agent with memory service.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	appName := "memory-chat"
	agentName := "memory-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with memory capabilities. "+
			"I can remember important information about you and recall it when needed."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(memoryService.Tools()), // Step 1: Prepare memory tools and instruction.
	)

	// Create runner.
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithMemoryService(memoryService), // Step 2: Set memory service.
	)

	fmt.Printf("✅ Memory chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *memoryChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /memory   - Show user memories")
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
		case "/memory":
			userInput = "show what you remember about me"
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
func (c *memoryChat) processMessage(ctx context.Context, userMessage string) error {
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
func (c *memoryChat) processResponse(eventChan <-chan *event.Event) error {
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

		// Handle tool calls.
		if c.hasToolCalls(event) {
			toolCallsDetected = true
			c.handleToolCalls(event, assistantStarted)
			assistantStarted = true
			continue
		}

		// Handle tool responses.
		if c.hasToolResponses(event) {
			c.handleToolResponses(event)
			continue
		}

		// Handle content.
		if content := c.extractContent(event); content != "" {
			if !assistantStarted {
				if toolCallsDetected {
					fmt.Printf("\n🤖 Assistant: ")
				}
				assistantStarted = true
			}
			fmt.Print(content)
			fullContent += content
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// hasToolCalls checks if the event contains tool calls.
func (c *memoryChat) hasToolCalls(event *event.Event) bool {
	return len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0
}

// hasToolResponses checks if the event contains tool responses.
func (c *memoryChat) hasToolResponses(event *event.Event) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			return true
		}
	}
	return false
}

// handleToolCalls displays tool call information.
func (c *memoryChat) handleToolCalls(event *event.Event, assistantStarted bool) {
	if assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Memory tool calls initiated:\n")
	for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
		}
	}
	fmt.Printf("\n🔄 Executing memory tools...\n")
}

// handleToolResponses displays tool response information.
func (c *memoryChat) handleToolResponses(event *event.Event) {
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ Memory tool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
		}
	}
}

// extractContent extracts content from the event based on streaming mode.
func (c *memoryChat) extractContent(event *event.Event) string {
	if len(event.Response.Choices) == 0 {
		return ""
	}

	choice := event.Response.Choices[0]
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

// startNewSession creates a new session ID.
func (c *memoryChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("memory-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new memory session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Memory and conversation history have been reset)\n")
	fmt.Println()
}

// Helper functions for creating pointers to primitive types.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
