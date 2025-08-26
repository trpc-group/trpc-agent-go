//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn chat using the Runner with streaming
// output, session management, and summarization.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var (
	modelName  = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming  = flag.Bool("streaming", true, "Enable streaming mode for responses")
	turnsToSum = flag.Int("turns", 6, "Approximate number of turns before summarization")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🧾 Session Summarizer Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	chat := &summarizerChat{
		modelName:  *modelName,
		streaming:  *streaming,
		turnsToSum: *turnsToSum,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

// summarizerChat manages the conversation focusing on summarization.
type summarizerChat struct {
	modelName  string
	streaming  bool
	turnsToSum int
	runner     runner.Runner
	mgr        summary.SummarizerManager
	sessSvc    session.Service
	userID     string
	sessionID  string
}

// run starts the interactive chat session.
func (c *summarizerChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	return c.startChat(ctx)
}

// setup creates runner, session service, summarizer manager and agent.
func (c *summarizerChat) setup(_ context.Context) error {
	// Create model and agent.
	mdl := openai.New(c.modelName)
	llm := llmagent.New(
		"chat-assistant",
		llmagent.WithModel(mdl),
		llmagent.WithDescription("A helpful AI assistant focusing on summarization."),
		llmagent.WithInstruction("Answer the user's questions concisely and helpfully."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1500),
			Temperature: floatPtr(0.6),
			Stream:      c.streaming,
		}),
	)

	// Session service.
	c.sessSvc = inmemory.NewSessionService()

	// Configure summarizer and manager.
	sum := summary.NewSummarizer(
		summary.WithModel(mdl),
		summary.WithKeepRecentCount(2),
		summary.WithChecksAny([]summary.Checker{
			summary.SetEventThreshold(c.turnsToSum * 2), // User + assistant events per turn.
			summary.SetTimeThreshold(5 * time.Minute),
		}),
	)
	c.mgr = summary.NewManager(sum)
	// Attach summarizer to the session service for service-based triggers.
	c.sessSvc = inmemory.NewSessionService(inmemory.WithSummarizerManager(c.mgr))

	// Create runner with summarizer.
	c.runner = runner.NewRunner(
		"summarizer-demo",
		llm,
		runner.WithSessionService(c.sessSvc),
	)

	// Identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-%d", time.Now().Unix())
	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (c *summarizerChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /summary  - Show current cached summary")
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
		case "/new":
			c.startNewSession()
			continue
		case "/summary":
			c.printCurrentSummary(ctx)
			continue
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // spacing between turns.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processMessage handles a single message exchange.
func (c *summarizerChat) processMessage(ctx context.Context, userMessage string) error {
	msg := model.NewUserMessage(userMessage)
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

// processResponse handles streaming and non-streaming responses.
func (c *summarizerChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	var full string
	for ev := range eventChan {
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			if c.streaming {
				piece := ev.Response.Choices[0].Delta.Content
				full += piece
				fmt.Print(piece)
			} else {
				full = ev.Response.Choices[0].Message.Content
			}
			if ev.Done && !c.streaming {
				fmt.Print(full)
			}
		}
	}
	return nil
}

// printCurrentSummary fetches summary from manager cache and prints it.
func (c *summarizerChat) printCurrentSummary(ctx context.Context) {
	key := session.Key{AppName: "summarizer-demo", UserID: c.userID, SessionID: c.sessionID}
	sess, err := c.sessSvc.GetSession(ctx, key)
	if err != nil || sess == nil {
		fmt.Println("No session found.")
		return
	}
	if text, ok := c.sessSvc.GetSessionSummaryText(ctx, sess); ok {
		fmt.Printf("📄 Summary:\n%s\n", text)
		return
	}
	fmt.Println("No summary available yet.")
}

// startNewSession creates a new session ID.
func (c *summarizerChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("chat-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
