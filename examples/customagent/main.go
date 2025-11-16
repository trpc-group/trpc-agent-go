//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates implementing a custom Agent by hand without Graph.
// It performs a simple intent classification and branches the logic:
// - chitchat: reply conversationally
// - task: provide a short actionable plan
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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 Custom Agent (intent-branching)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	// Build model and custom agent.
	m := openai.New(*modelName)
	ag := NewSimpleIntentAgent(
		"biz-agent",
		"A custom agent demonstrating business flow branching by intent",
		m,
	)

	// Use Runner for session + event handling.
	r := runner.NewRunner("customagent-app", ag)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	ctx := context.Background()

	chat := &interactiveChat{
		runner:    r,
		modelName: *modelName,
		userID:    "user",
		sessionID: fmt.Sprintf("custom-session-%d", time.Now().Unix()),
	}

	if err := chat.start(ctx); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

type interactiveChat struct {
	runner    runner.Runner
	modelName string
	userID    string
	sessionID string
}

func (c *interactiveChat) start(ctx context.Context) error {
	fmt.Printf("✅ Chat ready! Session: %s\n", c.sessionID)
	fmt.Println()
	fmt.Println("💡 Commands: /history, /new, /exit")
	fmt.Println()

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
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Bye!")
			return nil
		case "/new":
			c.startNewSession()
			continue
		case "/history":
			userInput = "show our conversation history"
		}

		if err := c.handle(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (c *interactiveChat) handle(ctx context.Context, text string) error {
	fmt.Print("🤖 Assistant: ")
	ch, err := c.runner.Run(ctx, c.userID, c.sessionID, model.NewUserMessage(text))
	if err != nil {
		return err
	}
	finished := false
	for evt := range ch {
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}
		if !finished {
			printContent(evt)
		}
		if evt.Done && !isToolLike(evt) {
			finished = true
		}
	}
	return nil
}

func (c *interactiveChat) startNewSession() {
	old := c.sessionID
	c.sessionID = fmt.Sprintf("custom-session-%d", time.Now().Unix())
	fmt.Printf("🆕 New session started.\n   Previous: %s\n   Current:  %s\n\n", old, c.sessionID)
}

func printContent(evt *event.Event) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	c := evt.Response.Choices[0]
	// Default streaming: print only delta to avoid duplicating final content.
	if c.Delta.Content != "" {
		fmt.Print(c.Delta.Content)
	}
}

func isToolLike(evt *event.Event) bool {
	if evt.Response == nil {
		return false
	}
	// Minimal check: tool calls or tool role messages.
	if len(evt.Response.Choices) > 0 {
		ch := evt.Response.Choices[0]
		if len(ch.Message.ToolCalls) > 0 {
			return true
		}
		if ch.Message.Role == model.RoleTool {
			return true
		}
	}
	return false
}
