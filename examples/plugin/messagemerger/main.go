//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates message normalization with the message merger
// plugin against a real OpenAI-compatible backend.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName       = "message-merger-demo"
	agentName     = "message-merger-assistant"
	userID        = "demo-user"
	envOpenAIKey  = "OPENAI_API_KEY"
	envOpenAIBase = "OPENAI_BASE_URL"
)

var (
	modelName = flag.String(
		"model",
		"gpt-4o-mini",
		"Name of the model to use",
	)
)

func main() {
	flag.Parse()
	history := demoHistory()
	fmt.Println("Message Merger Plugin Demo")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Auth: OpenAI SDK reads %s and %s from env\n", envOpenAIKey, envOpenAIBase)
	if os.Getenv(envOpenAIKey) == "" {
		fmt.Printf("Hint: %s is not set. Configure a real OpenAI-compatible backend before running this example.\n", envOpenAIKey)
	}
	fmt.Println(strings.Repeat("=", 72))
	printMessages("Caller-supplied messages", history)
	fmt.Println()
	fmt.Println("Running the runner with messagemerger enabled.")
	if err := runScenario(history); err != nil {
		fmt.Printf("Run failed: %v\n", err)
		os.Exit(1)
	}
}

func demoHistory() []model.Message {
	return []model.Message{
		model.NewUserMessage("Plan a one-day Hangzhou trip for me."),
		model.NewUserMessage("I prefer trains and a budget under 500 RMB."),
		model.NewAssistantMessage("Sure."),
		model.NewAssistantMessage("What city are you departing from?"),
		model.NewUserMessage("I will depart from Shanghai."),
	}
}

func runScenario(history []model.Message) error {
	r := newRunner()
	defer r.Close()
	sessionID := fmt.Sprintf("with-plugin-%d", time.Now().UnixNano())
	evCh, err := runner.RunWithMessages(
		context.Background(),
		r,
		userID,
		sessionID,
		history,
	)
	if err != nil {
		return err
	}
	finalResponse, err := collectFinalResponse(evCh)
	if err != nil {
		return err
	}
	fmt.Printf("Assistant: %s\n", finalResponse)
	return nil
}
