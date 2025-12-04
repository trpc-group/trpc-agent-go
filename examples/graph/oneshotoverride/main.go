//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the OneShot override pattern using the graph
// package with GraphAgent and Runner. It sets one_shot_messages for a single
// round to completely control the model input, then verifies it is cleared.
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
	appName          = "oneshot-override"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	inputFlag = flag.String("input", "",
		"User input to place in one_shot_messages. If empty, read stdin")
	sysFlag = flag.String("sys", "You are a domain expert. Use clear steps.",
		"System prompt to place in one_shot_messages")
)

func main() {
	flag.Parse()
	fmt.Printf("🚀 OneShot Override Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	content := strings.TrimSpace(*inputFlag)
	if content == "" {
		var err error
		content, err = readSingleLine()
		if err != nil {
			log.Fatalf("failed to read input: %v", err)
		}
	}

	if err := runOnce(content, *sysFlag); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

func readSingleLine() (string, error) {
	fmt.Print("💬 Enter your prompt (for OneShot): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("input scanner error: %w", err)
		}
		return "", fmt.Errorf("no input provided")
	}
	return strings.TrimSpace(scanner.Text()), nil
}

func runOnce(userText string, sysText string) error {
	ctx := context.Background()

	schema := graph.MessagesStateSchema()
	modelInstance := openai.New(*modelName)

	stateGraph := graph.NewStateGraph(schema)
	stateGraph.
		AddNode("set_oneshot", func(ctx context.Context, state graph.State) (any, error) {
			// Create one_shot_messages with system + user for this round.
			return graph.State{
				graph.StateKeyOneShotMessages: []model.Message{
					model.NewSystemMessage(sysText),
					model.NewUserMessage(userText),
				},
			}, nil
		}).
		AddLLMNode("ask", modelInstance,
			"Answer the user's question. Be concise.",
			map[string]tool.Tool{}).
		AddNode("verify", verifyOneShotCleared).
		SetEntryPoint("set_oneshot").
		SetFinishPoint("verify")
	stateGraph.AddEdge("set_oneshot", "ask")
	stateGraph.AddEdge("ask", "verify")

	compiled, err := stateGraph.Compile()
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	gagent, err := graphagent.New("oneshot-agent", compiled,
		graphagent.WithDescription("Agent using OneShot override for one round."),
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
	sessionID := fmt.Sprintf("oneshot-%d", time.Now().Unix())
	// Pass an empty user message; content is provided via OneShot.
	msg := model.NewUserMessage("")

	ch, err := r.Run(
		ctx,
		userID,
		sessionID,
		msg,
		agent.WithRuntimeState(map[string]any{"user_id": userID}),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return processEvents(ch)
}

func processEvents(eventChan <-chan *event.Event) error {
	var started bool
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}
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
		if ev.Done && ev.Response != nil && len(ev.Response.Choices) > 0 {
			content := ev.Response.Choices[0].Message.Content
			if content != "" {
				fmt.Printf("\n✅ Completed: %s\n", truncate(content, 10))
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

// verifyOneShotCleared confirms that one_shot_messages is cleared after the
// LLM node and prints a short note to the console.
func verifyOneShotCleared(ctx context.Context, state graph.State) (any, error) {
	var remaining int
	if v, ok := state[graph.StateKeyOneShotMessages].([]model.Message); ok {
		remaining = len(v)
	}
	if remaining == 0 {
		fmt.Println("🔍 Verification: one_shot_messages cleared after execution.")
	} else {
		fmt.Printf("🔍 Verification: one_shot_messages NOT cleared (len=%d).\n", remaining)
	}
	return nil, nil
}
