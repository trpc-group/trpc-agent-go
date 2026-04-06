//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the `subtask` tool for ephemeral call-return
// delegation: run a sub-task in an isolated context, result returns to the
// parent agent.
//
// Usage:
//
//	export OPENAI_BASE_URL="https://api.openai.com/v1"
//	export OPENAI_API_KEY="your-key"
//	go run . -model=gpt-4o-mini
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
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
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "gpt-4o-mini", "Model name")
	streaming = flag.Bool("streaming", true, "Enable streaming")
	variant   = flag.String("variant", "openai", "OpenAI provider variant")
)

const (
	appName   = "subtask-demo"
	agentName = "main-assistant"
)

func main() {
	flag.Parse()

	fmt.Println("Subtask Delegation Demo")
	fmt.Printf("Model: %s | Streaming: %t\n", *modelName, *streaming)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Built-in dynamic tools:")
	fmt.Println("  subtask    - Ephemeral sub-task (call-return, isolated context)")
	fmt.Println("Other tools:")
	fmt.Println("  calculator - Basic math")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Try:")
	fmt.Println("  'Use the subtask tool to calculate 2^10 * 3^5 step by step'")
	fmt.Println("Type '/exit' to quit")
	fmt.Println(strings.Repeat("=", 60))

	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	modelInstance := openai.New(*modelName,
		openai.WithVariant(openai.Variant(*variant)),
	)

	sessionService := sessioninmemory.NewSessionService()

	calcTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic math (add, subtract, multiply, divide, power)"),
	)

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(
			"You are a helpful assistant with subtask delegation capability.\n"+
				"Use the `subtask` tool to delegate complex sub-tasks that involve "+
				"heavy intermediate reasoning. The sub-agent works in an isolated context "+
				"so its intermediate steps don't pollute your main context.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(4000),
			Stream:    *streaming,
		}),
		llmagent.WithTools([]tool.Tool{calcTool}),
		llmagent.WithSubtask(),
	)

	r := runner.NewRunner(appName, llmAgent, runner.WithSessionService(sessionService))
	defer r.Close()

	userID := "demo-user"
	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n\n", sessionID)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/exit" {
			fmt.Println("Goodbye!")
			return nil
		}

		evCh, err := r.Run(ctx, userID, sessionID,
			model.NewUserMessage(input),
			agent.WithRequestID(uuid.NewString()),
		)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}

		fmt.Print("Assistant: ")
		printEvents(evCh)
		fmt.Println()
	}
	return scanner.Err()
}

func printEvents(evCh <-chan *event.Event) {
	for evt := range evCh {
		if evt.Error != nil {
			fmt.Printf("\n[Error] %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]

		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				fmt.Printf("\n  [tool-call] %s(%s)\n", tc.Function.Name, truncate(string(tc.Function.Arguments), 200))
			}
			continue
		}

		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("  [tool-result] %s\n", truncate(choice.Message.Content, 300))
			continue
		}

		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		} else if choice.Message.Content != "" && choice.Message.Role == model.RoleAssistant {
			fmt.Print(choice.Message.Content)
		}
	}
	fmt.Println()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type calcArgs struct {
	Operation string  `json:"operation" jsonschema:"description=Operation: add subtract multiply divide power"`
	A         float64 `json:"a" jsonschema:"description=First number"`
	B         float64 `json:"b" jsonschema:"description=Second number"`
}

type calcResult struct {
	Result float64 `json:"result"`
}

func calculate(_ context.Context, args calcArgs) (calcResult, error) {
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		return calcResult{Result: args.A + args.B}, nil
	case "subtract", "-":
		return calcResult{Result: args.A - args.B}, nil
	case "multiply", "*":
		return calcResult{Result: args.A * args.B}, nil
	case "divide", "/":
		if args.B == 0 {
			return calcResult{}, fmt.Errorf("division by zero")
		}
		return calcResult{Result: args.A / args.B}, nil
	case "power", "^":
		return calcResult{Result: math.Pow(args.A, args.B)}, nil
	default:
		return calcResult{}, fmt.Errorf("unknown operation: %s", args.Operation)
	}
}

func intPtr(i int) *int { return &i }
