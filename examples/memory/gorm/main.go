//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates episodic memory backed by a shared *gorm.DB.
//
// Usage:
//
//	# SQLite file (default; AutoMigrate creates the memories table)
//	go run .
//
//	# PostgreSQL (host-owned DDL — table must exist; see examples/memory/gorm/README.md)
//	export GORM_DSN="postgres://user:pass@localhost:5432/app?sslmode=disable"
//	go run . -skip-db-init
//
// Environment:
//
//	OPENAI_API_KEY   required for the chat demo
//	GORM_DSN         optional; SQLite path or postgres:// DSN (default: memories_gorm.db)
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	util "trpc.group/trpc-go/trpc-agent-go/examples/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorygorm "trpc.group/trpc-go/trpc-agent-go/memory/gorm"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName  = flag.String("model", "deepseek-v4-flash", "Chat model name")
	sqliteFile = flag.String("sqlite", "memories_gorm.db", "SQLite database file when GORM_DSN is unset")
	skipDBInit = flag.Bool("skip-db-init", false, "Skip AutoMigrate (use when the host application owns DDL)")
	softDelete = flag.Bool("soft-delete", false, "Enable soft delete via deleted_at")
	streaming  = flag.Bool("streaming", true, "Stream assistant responses")
)

func main() {
	flag.Parse()

	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	db, dsnLabel, err := openGormDB()
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("resolve sql.DB: %v", err)
	}
	defer sqlDB.Close()

	memorySvc, err := newGormMemoryService(db)
	if err != nil {
		log.Fatalf("create memory service: %v", err)
	}
	defer memorySvc.Close()

	fmt.Printf("🧠 GORM Memory Chat\n")
	fmt.Printf("Database: %s\n", dsnLabel)
	fmt.Printf("Skip DB init: %t\n", *skipDBInit)
	fmt.Printf("Soft delete: %t\n", *softDelete)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	chat := &gormMemoryChat{
		memorySvc: memorySvc,
		streaming: *streaming,
		userID:    "demo-user",
		sessionID: fmt.Sprintf("gorm-session-%d", time.Now().Unix()),
	}

	if err := chat.run(context.Background()); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

func openGormDB() (*gorm.DB, string, error) {
	if dsn := strings.TrimSpace(os.Getenv("GORM_DSN")); dsn != "" {
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			return db, redactDSN(dsn), err
		}
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		return db, "sqlite:" + dsn, err
	}

	db, err := gorm.Open(sqlite.Open(*sqliteFile), &gorm.Config{})
	return db, "sqlite:" + *sqliteFile, err
}

func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), "****")
	return u.String()
}

func newGormMemoryService(db *gorm.DB) (memory.Service, error) {
	opts := []memorygorm.ServiceOpt{
		memorygorm.WithToolEnabled(memory.AddToolName, true),
		memorygorm.WithToolEnabled(memory.UpdateToolName, true),
		memorygorm.WithToolEnabled(memory.SearchToolName, true),
		memorygorm.WithToolEnabled(memory.LoadToolName, true),
	}
	if *skipDBInit {
		opts = append(opts, memorygorm.WithSkipDBInit(true))
	}
	if *softDelete {
		opts = append(opts, memorygorm.WithSoftDelete(true))
	}
	return memorygorm.NewService(append([]memorygorm.ServiceOpt{memorygorm.WithDB(db)}, opts...)...)
}

type gormMemoryChat struct {
	memorySvc memory.Service
	runner    runner.Runner
	streaming bool
	userID    string
	sessionID string
}

func (c *gormMemoryChat) run(ctx context.Context) error {
	modelInstance := openai.New(*modelName)
	llmAgent := llmagent.New(
		"gorm-memory-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful assistant with GORM-backed episodic memory."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: util.IntPtr(2000),
			Stream:    c.streaming,
		}),
		llmagent.WithTools(c.memorySvc.Tools()),
	)

	c.runner = runner.NewRunner(
		"gorm-memory-chat",
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithMemoryService(c.memorySvc),
	)
	defer c.runner.Close()

	fmt.Printf("✅ Ready. Session: %s\n\n", c.sessionID)
	return c.startChat(ctx)
}

func (c *gormMemoryChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Commands: /memory  /new  /exit")
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
			c.sessionID = fmt.Sprintf("gorm-session-%d", time.Now().Unix())
			fmt.Printf("🆕 New session: %s (memories preserved)\n\n", c.sessionID)
			continue
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner: %w", err)
	}
	return nil
}

func (c *gormMemoryChat) processMessage(ctx context.Context, userMessage string) error {
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, model.NewUserMessage(userMessage))
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *gormMemoryChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
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

		if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
			if assistantStarted {
				fmt.Println()
			}
			toolCallsDetected = true
			assistantStarted = true
			fmt.Printf("🔧 Memory tool calls:\n%s", util.FormatToolCalls(event.Response.Choices[0].Message.ToolCalls))
			fmt.Println("🔄 Executing...")
			continue
		}

		if util.FormatToolResponses(event.Response.Choices) != "" {
			fmt.Print(util.FormatToolResponses(event.Response.Choices))
			continue
		}

		content := ""
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			if c.streaming {
				content = choice.Delta.Content
			} else {
				content = choice.Message.Content
			}
		}
		if content == "" {
			if event.IsFinalResponse() {
				fmt.Println()
				finalSeen = true
			}
			continue
		}

		if !assistantStarted {
			if toolCallsDetected {
				fmt.Print("🤖 Assistant: ")
			}
			assistantStarted = true
		}
		fmt.Print(content)

		if event.IsFinalResponse() {
			fmt.Println()
			finalSeen = true
		}
	}

	return nil
}
