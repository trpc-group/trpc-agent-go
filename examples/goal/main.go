//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the goal LLMAgent extension.
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

	goalext "trpc.group/trpc-go/trpc-agent-go/agent/extension/goal"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName  = flag.String("model", "deepseek-v4-flash", "Name of the model to use")
	maxRetries = flag.Int("max-retries", 3, "Maximum blocked final responses before fail-open")
	streaming  = flag.Bool("streaming", true, "Enable streaming model responses")
	temp       = flag.Float64("temperature", -1, "Sampling temperature; negative uses the model default")
)

func main() {
	flag.Parse()

	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set. Export it before running.")
		fmt.Println(`  export OPENAI_BASE_URL="https://api.openai.com/v1"`)
		fmt.Println(`  export OPENAI_API_KEY="<your-key>"`)
		fmt.Println()
		os.Exit(1)
	}

	if err := run(context.Background()); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

func run(ctx context.Context) error {
	modelInstance := openai.New(*modelName)
	sessionService := inmemory.NewSessionService()
	appName := "goal-extension-demo"
	agentName := "goal-assistant"
	genConfig := model.GenerationConfig{
		Stream: *streaming,
	}
	if *temp >= 0 {
		genConfig.Temperature = floatPtr(*temp)
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A focused assistant that can work toward session goals."),
		llmagent.WithInstruction(
			"Work concretely. When goal tools are available, use them only according to their descriptions.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithExtensions(goalext.New(
			goalext.WithMaxRetries(*maxRetries),
		)),
	)

	r := runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("goal-session-%d", time.Now().Unix())

	fmt.Println("Goal Extension Example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Max retries: %d\n", *maxRetries)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println("Try: /goal Draft a release checklist, verify risks, and produce a final summary.")
	fmt.Println("Type exit to quit.")
	fmt.Println(strings.Repeat("=", 72))

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "exit") {
			return nil
		}
		if err := runTurn(ctx, r, sessionService, appName, userID, sessionID, text); err != nil {
			fmt.Printf("\nError: %v\n", err)
		}
	}
	return scanner.Err()
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	sessionService session.Service,
	appName string,
	userID string,
	sessionID string,
	text string,
) error {
	if objective, ok := parseGoalCommand(text); ok {
		if objective == "" {
			return fmt.Errorf("usage: /goal <objective>")
		}
		key := session.Key{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
		}
		g, err := goalext.Start(ctx, sessionService, key, objective)
		if err != nil {
			return err
		}
		fmt.Printf("[goal created] %s\n", g.Objective)
		text = "Continue working toward the active session goal: " + g.Objective
	}

	eventCh, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(text))
	if err != nil {
		return err
	}

	for ev := range eventCh {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("\nError event: %s\n", ev.Error.Message)
			continue
		}
		if ev.IsRunnerCompletion() {
			fmt.Println("\n[runner completion]")
			continue
		}
		printEventText(ev, *streaming)
		printToolCalls(ev)
	}
	fmt.Println("[goal stream closed]")
	return nil
}

func parseGoalCommand(text string) (string, bool) {
	const prefix = "/goal"
	if text == prefix {
		return "", true
	}
	if !strings.HasPrefix(text, prefix+" ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true
}

func printEventText(ev *event.Event, streaming bool) {
	if ev.Response == nil || len(ev.Choices) == 0 {
		return
	}
	choice := ev.Choices[0]
	if streaming {
		if choice.Delta.Role != "" && choice.Delta.Role != model.RoleAssistant {
			return
		}
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		}
		return
	}
	if choice.Message.Role != "" && choice.Message.Role != model.RoleAssistant {
		return
	}
	if choice.Message.Content != "" {
		fmt.Println(choice.Message.Content)
	}
}

func printToolCalls(ev *event.Event) {
	if ev.Response == nil || len(ev.Choices) == 0 {
		return
	}
	for _, call := range ev.Choices[0].Message.ToolCalls {
		fmt.Printf("\n[tool call] %s %s\n", call.Function.Name, string(call.Function.Arguments))
	}
}

func floatPtr(v float64) *float64 { return &v }
