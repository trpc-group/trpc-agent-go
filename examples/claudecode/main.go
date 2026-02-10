//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using the Claude Code CLI agent with the runner.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	claudeBin    = flag.String("claude-bin", "claude", "Claude Code CLI executable path")
	outputFormat = flag.String("output-format", "json", "Transcript output format: json or stream-json")
	logDir       = flag.String("log-dir", "log", "Persist raw stdout/stderr logs under this directory")
)

func main() {
	flag.Parse()
	ag, err := newClaudeAgent(*claudeBin, *outputFormat, *logDir)
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	r := runner.NewRunner("claudecode-cli-example", ag)
	defer r.Close()
	ctx := context.Background()
	runInteractive(ctx, r)
}

func runInteractive(ctx context.Context, r runner.Runner) {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Type '/exit' to quit.")
	userID := "demo-user"
	sessionID := uuid.NewString()
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "/exit") {
			return
		}
		if err := runOnce(ctx, r, userID, sessionID, line); err != nil {
			fmt.Printf("Run failed: %v\n", err)
		}
	}
}

func runOnce(ctx context.Context, r runner.Runner, userID, sessionID, prompt string) error {
	ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return err
	}
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("Error: %s (%s)\n", evt.Error.Message, evt.Error.Type)
			continue
		}
		printToolEvents(evt)
		if evt.IsFinalResponse() && len(evt.Choices) > 0 {
			fmt.Printf("Assistant: %s\n", strings.TrimSpace(evt.Choices[0].Message.Content))
		}
	}
	return nil
}

func printToolEvents(evt *event.Event) {
	if evt.Object == model.ObjectTypeTransfer && evt.ContainsTag(event.TransferTag) {
		fmt.Printf("Transfer: %s\n", strings.TrimSpace(evt.Choices[0].Message.Content))
		return
	}
	if evt.IsToolCallResponse() && len(evt.Choices) > 0 {
		for _, call := range evt.Choices[0].Message.ToolCalls {
			args := strings.TrimSpace(string(call.Function.Arguments))
			if args == "" {
				fmt.Printf("Tool call: %s\n", call.Function.Name)
				continue
			}
			fmt.Printf("Tool call: %s args=%s\n", call.Function.Name, args)
		}
	}
	if evt.IsToolResultResponse() && len(evt.Choices) > 0 {
		name := strings.TrimSpace(evt.Choices[0].Message.ToolName)
		content := strings.TrimSpace(evt.Choices[0].Message.Content)
		if name == "" {
			name = "unknown"
		}
		if content == "" {
			fmt.Printf("Tool result: %s\n", name)
			return
		}
		fmt.Printf("Tool result: %s content=%s\n", name, content)
	}
}
