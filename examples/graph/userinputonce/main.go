//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a one-shot user input workflow using the graph
// package with GraphAgent and Runner. It shows how user input is consumed
// exactly once, then cleared from state by the LLM node execution.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultModelName = "deepseek-chat"
	appName          = "userinputonce"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	inputFlag = flag.String("input", "",
		"User input to process. If empty, read from stdin once")
)

func main() {
	flag.Parse()
	fmt.Printf("🚀 One-shot User Input Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	content := *inputFlag
	if strings.TrimSpace(content) == "" {
		var err error
		content, err = readSingleLine()
		if err != nil {
			log.Fatalf("failed to read input: %v", err)
		}
	}

	if err := runOnce(content); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

func readSingleLine() (string, error) {
	fmt.Print("💬 Enter your prompt: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("input scanner error: %w", err)
		}
		return "", fmt.Errorf("no input provided")
	}
	return strings.TrimSpace(scanner.Text()), nil
}

func runOnce(content string) error {
	ctx := context.Background()

	// Build graph with a single LLM node. After execution, user input is
	// cleared by the LLM node per graph/state_graph.go behavior.
	schema := graph.MessagesStateSchema()
	modelInstance := openai.New(*modelName)

	stateGraph := graph.NewStateGraph(schema)
	stateGraph.
		AddLLMNode("ask", modelInstance,
			"You are a helpful assistant. Answer concisely.",
			map[string]tool.Tool{}).
		AddNode("verify", verifyCleared).
		SetEntryPoint("ask").
		SetFinishPoint("verify")
	stateGraph.AddEdge("ask", "verify")

	compiled, err := stateGraph.Compile()
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	// Create agent and runner.
	gagent, err := graphagent.New("one-shot-agent", compiled,
		graphagent.WithDescription(
			"Agent that consumes user input once and clears it."),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		gagent,
		runner.WithSessionService(sessionService),
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("once-%d", time.Now().Unix())

	// Create user message and run once.
	message := model.NewUserMessage(content)
	eventChan, err := r.Run(
		ctx,
		userID,
		sessionID,
		message,
		agent.WithRuntimeState(map[string]any{"user_id": userID}),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return processEvents(eventChan)
}

func processEvents(eventChan <-chan *event.Event) error {
	var started bool
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}
		// Print streaming tokens from model.
		if len(ev.Choices) > 0 {
			ch := ev.Choices[0]
			if ch.Delta.Content != "" {
				if !started {
					fmt.Print("🤖: ")
					started = true
				}
				fmt.Print(ch.Delta.Content)
			}
			if ch.Delta.Content == "" && started {
				fmt.Println()
				started = false
			}
		}
		// When finished, show final response if present.
		if ev.Done && ev.Response != nil && len(ev.Response.Choices) > 0 {
			content := ev.Response.Choices[0].Message.Content
			if content != "" {
				fmt.Printf("\n✅ Completed: %s\n", truncate(content, 120))
			}
		}
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// verifyCleared is a function node that logs whether user_input has been
// cleared after the LLM node. This demonstrates the one-shot behavior in a
// concrete way without inspecting internal state directly.
func verifyCleared(ctx context.Context, state graph.State) (any, error) {
	var userInput string
	if v, ok := state[graph.StateKeyUserInput].(string); ok {
		userInput = v
	}
	if userInput == "" {
		fmt.Println("🔍 Verification: user_input is cleared (" +
			"LLM consumed it once).")
	} else {
		fmt.Println("🔍 Verification: user_input is NOT cleared.")
	}
	return nil, nil
}
