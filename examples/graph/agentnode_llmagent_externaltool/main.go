//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates GraphAgent resume after an AgentNode LLMAgent external tool call.
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

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName = "agentnode-llmagent-externaltool"
	userID  = "agentnode-llmagent-externaltool-user"
)

var (
	modelName  = flag.String("model", "deepseek-v4-flash", "OpenAI-compatible model name.")
	baseURL    = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL.")
	apiKey     = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key for the model service.")
	toolResult = flag.String("tool-result", "", "External search result content.")
)

func main() {
	flag.Parse()
	maxTokens := 1024
	temperature := 0.0
	ag, err := newGraphAgent(openai.New(
		*modelName,
		openai.WithBaseURL(*baseURL),
		openai.WithAPIKey(*apiKey),
	), model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
	})
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}
	r := runner.NewRunner(appName, ag, runner.WithSessionService(sessioninmemory.NewSessionService()))
	defer r.Close()
	ctx := context.Background()
	sessionID := fmt.Sprintf("agentnode-llmagent-externaltool-session-%d", time.Now().UnixNano())
	scanner := bufio.NewScanner(os.Stdin)
	turn := 1
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println("Type a request to start a turn. Type /exit to quit.")
	for {
		request, ok, err := readUserRequest(scanner)
		if err != nil {
			log.Fatalf("read input failed: %v", err)
		}
		if !ok {
			return
		}
		if err := runTurn(ctx, r, scanner, sessionID, turn, request); err != nil {
			log.Printf("turn %d failed: %v", turn, err)
		}
		turn++
	}
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	scanner *bufio.Scanner,
	sessionID string,
	turn int,
	request string,
) error {
	lineageID := fmt.Sprintf("%s-turn-%d-%d", appName, turn, time.Now().UnixNano())
	fmt.Printf("\nTurn #%d: waiting for external_search interrupt.\n", turn)
	interrupt, err := runUntilInterrupt(ctx, r, sessionID, lineageID, request)
	if err != nil {
		return fmt.Errorf("run graph: %w", err)
	}
	fmt.Printf("toolCallId: %s\n", interrupt.Request.ToolCallID)
	fmt.Printf("toolArgs: %s\n", interrupt.Request.Args)
	fmt.Printf("checkpointId: %s\n", interrupt.CheckpointID)
	result, err := readToolResult(scanner)
	if err != nil {
		return fmt.Errorf("read external result: %w", err)
	}
	fmt.Printf("\nTurn #%d: resuming graph.\n", turn)
	return resumeAndPrint(ctx, r, sessionID, interrupt, result)
}

func readUserRequest(scanner *bufio.Scanner) (string, bool, error) {
	for {
		fmt.Print("user> ")
		if !scanner.Scan() {
			return "", false, scanner.Err()
		}
		request := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(request) {
		case "":
			continue
		case "/exit", "/quit":
			return "", false, nil
		default:
			return request, true, nil
		}
	}
}
