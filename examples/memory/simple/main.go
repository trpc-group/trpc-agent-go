//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates simple memory management using the Runner with
// streaming output, session management, and manual memory tool calling.
//
// Usage:
//
//	go run main.go -memory=inmemory
//	go run main.go -memory=sqlite
//	go run main.go -memory=sqlitevec
//	go run main.go -memory=redis
//	go run main.go -memory=mysql
//	go run main.go -memory=postgres
//	go run main.go -memory=pgvector
//
// Environment variables by memory type (example usage):
//
//	sqlite:
//		export SQLITE_MEMORY_DSN="file:memories.db?_busy_timeout=5000"
//
//	sqlitevec:
//		export SQLITEVEC_MEMORY_DSN="file:memories_vec.db?_busy_timeout=5000"
//		export SQLITEVEC_EMBEDDER_MODEL="text-embedding-3-small"
//
//	redis:
//		export REDIS_ADDR="localhost:6379"
//
//	mysql:
//		export MYSQL_HOST="localhost"
//		export MYSQL_PORT="3306"
//		export MYSQL_USER="root"
//		export MYSQL_PASSWORD=""
//		export MYSQL_DATABASE="trpc_agent_go"
//
//	postgres:
//		export PG_HOST="localhost"
//		export PG_PORT="5432"
//		export PG_USER="postgres"
//		export PG_PASSWORD=""
//		export PG_DATABASE="trpc_agent_go"
//
//	pgvector:
//		export PGVECTOR_HOST="localhost"
//		export PGVECTOR_PORT="5432"
//		export PGVECTOR_USER="postgres"
//		export PGVECTOR_PASSWORD=""
//		export PGVECTOR_DATABASE="trpc_agent_go"
//		export PGVECTOR_EMBEDDER_MODEL="text-embedding-3-small"
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
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	util "trpc.group/trpc-go/trpc-agent-go/examples/memory"
)

var (
	modelName = flag.String(
		"model",
		"deepseek-chat",
		"Name of the model to use",
	)
	memServiceName = flag.String(
		"memory",
		"inmemory",
		"Name of the memory service to use, "+
			"inmemory / sqlite / sqlitevec / redis / "+
			"mysql / postgres / pgvector",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode for responses",
	)
	softDelete = flag.Bool(
		"soft-delete",
		false,
		"Enable soft delete for SQLite/SQLiteVec/"+
			"MySQL/PostgreSQL/pgvector memory service",
	)
)

func main() {
	flag.Parse()

	fmt.Printf("🧠 Simple Memory Chat\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Memory Service: %s\n", *memServiceName)

	memoryType := util.MemoryType(*memServiceName)
	util.PrintMemoryInfo(memoryType, *softDelete)

	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Available tools: %s\n", util.GetAvailableToolsString())
	fmt.Println(strings.Repeat("=", 50))

	chat := &memoryChat{
		modelName:      *modelName,
		memServiceName: *memServiceName,
		streaming:      *streaming,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type memoryChat struct {
	modelName      string
	memServiceName string
	streaming      bool
	runner         runner.Runner
	userID         string
	sessionID      string
}

func (c *memoryChat) run() error {
	ctx := context.Background()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	defer c.runner.Close()

	return c.startChat(ctx)
}

func (c *memoryChat) setup(_ context.Context) error {
	memoryType := util.MemoryType(c.memServiceName)

	memoryService, err := util.NewMemoryServiceByType(memoryType, util.MemoryServiceConfig{
		SoftDelete: *softDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to create memory service: %w", err)
	}

	c.userID = "user"
	c.sessionID = fmt.Sprintf("memory-session-%d", time.Now().Unix())

	genConfig := model.GenerationConfig{
		MaxTokens: util.IntPtr(2000),
		Stream:    c.streaming,
	}

	appName := "memory-chat"
	agentName := "memory-assistant"

	modelInstance := openai.New(c.modelName)

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with memory capabilities. "+
			"I can remember important information about you and recall it when needed."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(memoryService.Tools()),
	)

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithMemoryService(memoryService),
	)

	fmt.Printf("✅ Memory chat ready! Session: %s\n\n", c.sessionID)

	return nil
}

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

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

func (c *memoryChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return c.processResponse(eventChan)
}

func (c *memoryChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
		finalSeen         bool
	)

	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		if finalSeen {
			continue
		}

		if c.hasToolCalls(event) {
			toolCallsDetected = true
			c.handleToolCalls(event, assistantStarted)
			assistantStarted = true
			continue
		}

		if c.hasToolResponses(event) {
			c.handleToolResponses(event)
			continue
		}

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

		if event.IsFinalResponse() {
			fmt.Printf("\n")
			finalSeen = true
		}
	}

	return nil
}

func (c *memoryChat) hasToolCalls(event *event.Event) bool {
	return len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0
}

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

func (c *memoryChat) handleToolCalls(event *event.Event, assistantStarted bool) {
	if assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Memory tool calls initiated:\n")
	fmt.Printf("%s", util.FormatToolCalls(event.Response.Choices[0].Message.ToolCalls))
	fmt.Printf("\n🔄 Executing memory tools...\n")
}

func (c *memoryChat) handleToolResponses(event *event.Event) {
	fmt.Printf("%s", util.FormatToolResponses(event.Response.Choices))
}

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

func (c *memoryChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("memory-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new memory session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset, memories are preserved)\n")
	fmt.Println()
}
