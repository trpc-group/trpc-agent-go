//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates Pensieve-style context self-pruning using tool/context.
//
// The agent can check context budget, save distilled notes, list note keys,
// and mask processed events so they no longer appear in the LLM-visible history.
//
// Usage:
//
//	export OPENAI_API_KEY="your-api-key"
//	go run .
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
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	contexttools "trpc.group/trpc-go/trpc-agent-go/tool/context"
)

const (
	appName   = "pensieve-demo"
	agentName = "pensieve-assistant"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "Model name")
	streaming = flag.Bool("streaming", true, "Enable streaming responses")
)

func main() {
	flag.Parse()

	fmt.Println("Pensieve context pruning demo")
	fmt.Printf("Model: %s | Streaming: %t\n", *modelName, *streaming)
	fmt.Println("Tools: check_budget, note, notes_index, read_notes, delete_context")
	fmt.Println("Try: ask the agent to summarize the chat, save a note, then prune old turns.")
	fmt.Println(strings.Repeat("=", 50))

	chat := &pensieveChat{modelName: *modelName, streaming: *streaming}
	if err := chat.run(); err != nil {
		log.Fatalf("demo failed: %v", err)
	}
}

type pensieveChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
}

func (c *pensieveChat) run() error {
	ctx := context.Background()
	if err := c.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

func (c *pensieveChat) setup() error {
	modelInstance := openai.New(c.modelName)

	pensieveTools := contexttools.Tools()
	allTools := make([]tool.Tool, 0, len(pensieveTools))
	allTools = append(allTools, pensieveTools...)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.4),
		Stream:      c.streaming,
	}

	instruction := "You help manage long conversations using Pensieve context tools. " +
		"When context grows, call check_budget, distill important facts into note, " +
		"use notes_index to discover saved notes cheaply, read_notes when you need full bodies, " +
		"and delete_context to hide event IDs you have already processed. " +
		"Explain briefly what you pruned and what you kept in notes."

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Assistant with Pensieve context self-pruning tools."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(allTools),
	)

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)

	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("pensieve-session-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n\n", c.sessionID)
	return nil
}

func (c *pensieveChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Commands: /budget  /exit")
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "/exit":
			fmt.Println("Goodbye!")
			return nil
		case "/budget":
			input = "call check_budget and report visible vs masked events"
		}
		if err := c.processMessage(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (c *pensieveChat) processMessage(ctx context.Context, userMessage string) error {
	events, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
		agent.WithRequestID(fmt.Sprintf("req-%d", time.Now().UnixNano())),
	)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	return c.drainResponse(events)
}

func (c *pensieveChat) drainResponse(events <-chan *event.Event) error {
	fmt.Print("Assistant: ")
	var started bool
	for evt := range events {
		if evt.Error != nil {
			fmt.Printf("\nError: %s\n", evt.Error.Message)
			continue
		}
		if len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if len(choice.Message.ToolCalls) > 0 {
			if started {
				fmt.Println()
			}
			fmt.Println("Tool calls:")
			for _, tc := range choice.Message.ToolCalls {
				fmt.Printf("  - %s\n", tc.Function.Name)
			}
			started = true
			continue
		}
		content := choice.Delta.Content
		if !c.streaming {
			content = choice.Message.Content
		}
		if content == "" {
			continue
		}
		if !started {
			started = true
		}
		fmt.Print(content)
	}
	if started {
		fmt.Println()
	}
	return nil
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
