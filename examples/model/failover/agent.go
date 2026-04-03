//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type failoverChat struct {
	config    appConfig
	runner    runner.Runner
	userID    string
	sessionID string
}

func (c *failoverChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

func (c *failoverChat) setup(_ context.Context) error {
	failoverModel, err := newFailoverModel(c.config)
	if err != nil {
		return err
	}
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1200),
		Temperature: floatPtr(0.7),
		Stream:      c.config.streaming,
	}
	agentInstance := llmagent.New(
		agentName,
		llmagent.WithModel(failoverModel),
		llmagent.WithDescription("A chat assistant backed by a primary model with a backup failover model."),
		llmagent.WithInstruction("You are a concise and reliable assistant. Answer clearly and directly."),
		llmagent.WithGenerationConfig(genConfig),
	)
	c.runner = runner.NewRunner(
		appName,
		agentInstance,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	c.userID = "demo-user"
	c.sessionID = newSessionID()
	c.printBanner()
	return nil
}

func (c *failoverChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	printCommands()
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
		case "/new":
			c.startNewSession()
			continue
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
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

func (c *failoverChat) startNewSession() {
	c.sessionID = newSessionID()
	fmt.Printf("🆕 New session: %s\n\n", c.sessionID)
}

func (c *failoverChat) processMessage(ctx context.Context, userMessage string) error {
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
	)
	if err != nil {
		return fmt.Errorf("failed to run failover agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func newSessionID() string {
	return fmt.Sprintf("failover-session-%d", time.Now().UnixNano())
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
