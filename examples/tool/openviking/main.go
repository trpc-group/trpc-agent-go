//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive chat backed by the OpenViking
// context database (https://github.com/volcengine/OpenViking) exposed as agent
// tools. The agent follows OpenViking's "search then read" pattern: it locates
// relevant viking:// URIs with viking_search/viking_find, then reads full
// content with viking_read only where needed.
//
// Prerequisites: an OpenViking server running locally (openviking-server),
// reachable at the URL passed via -openviking (default http://localhost:1933).
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/openviking"
)

func main() {
	modelName := flag.String("model", "deepseek-v4-flash", "Name of the model to use")
	ovURL := flag.String("openviking", "http://localhost:1933", "OpenViking server URL")
	apiKey := flag.String("openviking-key", os.Getenv("OPENVIKING_API_KEY"), "OpenViking API key")
	profile := flag.String("profile", "agent", "Tool profile: retrieval | agent | admin")
	flag.Parse()

	fmt.Printf("OpenViking Tools Chat Demo\n")
	fmt.Printf("Model: %s | OpenViking: %s | Profile: %s\n", *modelName, *ovURL, *profile)
	fmt.Println(strings.Repeat("=", 50))

	ts, err := openviking.NewToolSet(
		openviking.WithBaseURL(*ovURL),
		openviking.WithAPIKey(*apiKey),
		openviking.WithProfile(openviking.Profile(*profile)),
	)
	if err != nil {
		log.Fatalf("failed to create OpenViking tool set: %v", err)
	}
	defer ts.Close()

	modelInstance := openai.New(*modelName)
	genConfig := model.GenerationConfig{
		Stream: true,
	}
	llmAgent := llmagent.New(
		"openviking-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An assistant with access to the OpenViking context database."),
		llmagent.WithInstruction("Use viking_search or viking_find to locate relevant viking:// URIs. "+
			"These return short summaries only. Then call viking_read on the most relevant URIs to read full content "+
			"before answering. Prefer reading overviews for large nodes."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithToolSets([]tool.ToolSet{ts}),
	)

	appRunner := runner.NewRunner("openviking-chat", llmAgent)
	defer appRunner.Close()

	userID := "user"
	sessionID := fmt.Sprintf("openviking-session-%d", time.Now().Unix())
	fmt.Printf("Ready. Session: %s (type 'exit' to quit)\n\n", sessionID)

	if err := chatLoop(context.Background(), appRunner, userID, sessionID); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

func chatLoop(ctx context.Context, appRunner runner.Runner, userID, sessionID string) error {
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
		if strings.ToLower(input) == "exit" {
			fmt.Println("Goodbye!")
			return nil
		}
		if err := processMessage(ctx, appRunner, userID, sessionID, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func processMessage(ctx context.Context, appRunner runner.Runner, userID, sessionID, text string) error {
	eventChan, err := appRunner.Run(ctx, userID, sessionID, model.NewUserMessage(text))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	fmt.Print("Assistant: ")
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n[error] %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]
			for _, tc := range choice.Message.ToolCalls {
				fmt.Printf("\n[tool] %s %s\n", tc.Function.Name, string(tc.Function.Arguments))
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}
		if ev.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}
