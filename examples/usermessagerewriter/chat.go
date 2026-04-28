//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// rewriterChat manages the demo conversation loop.
type rewriterChat struct {
	modelName      string
	streaming      bool
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

func (c *rewriterChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

// setup creates the runner and LLM agent used by the demo.
func (c *rewriterChat) setup(_ context.Context) error {
	c.sessionService = sessioninmemory.NewSessionService()
	c.runner = runner.NewRunner(
		appName,
		newAgent(c.modelName, c.streaming),
		runner.WithSessionService(c.sessionService),
	)
	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("rewriter-session-%d", time.Now().Unix())
	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// startChat runs the interactive terminal loop.
func (c *rewriterChat) startChat(ctx context.Context) error {
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
		if userInput == "/exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}
		if userInput == "/dump" {
			if err := c.dumpSession(ctx); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}
			fmt.Println()
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

// processMessage runs one conversation turn through the rewriter-enabled runner.
func (c *rewriterChat) processMessage(ctx context.Context, userInput string) error {
	rewritten := c.rewriteMessages(userInput)
	printRewritePreview(userInput, rewritten)
	message := model.NewUserMessage(userInput)
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		message,
		agent.WithUserMessageRewriter(c.rewriteUserMessage),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

// processResponse prints the assistant response from the event stream.
func (c *rewriterChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}
		if len(evt.Response.Choices) > 0 {
			if c.streaming {
				fmt.Print(evt.Response.Choices[0].Delta.Content)
			} else {
				fmt.Print(evt.Response.Choices[0].Message.Content)
			}
		}
		if evt.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}

// dumpSession prints the persisted session transcript for debugging.
func (c *rewriterChat) dumpSession(ctx context.Context) error {
	sess, err := c.sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}
	printSessionTranscript(sess)
	return nil
}
