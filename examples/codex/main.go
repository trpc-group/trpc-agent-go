//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using the Codex CLI agent with the runner.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/codex"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	codexBin       = flag.String("codex-bin", "codex", "Codex CLI executable path")
	codexModel     = flag.String("model", "", "Optional Codex model override")
	mcpURL         = flag.String("mcp-url", "http://localhost:3002/mcp", "Streamable HTTP MCP server URL")
	approvalPolicy = flag.String("approval-policy", "never", "Codex approval policy")
	codexSandbox   = flag.String("sandbox", "workspace-write", "Codex sandbox mode")
	codexWorkDir   = flag.String("work-dir", ".", "CLI process working directory")
	logDir         = flag.String("log-dir", "log", "Persist raw stdout/stderr logs under this directory")
)

func main() {
	flag.Parse()
	ag, err := newCodexAgent(codexSettings{
		bin:            *codexBin,
		model:          *codexModel,
		mcpURL:         *mcpURL,
		approvalPolicy: *approvalPolicy,
		sandbox:        *codexSandbox,
		workDir:        *codexWorkDir,
		logDir:         *logDir,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	r := runner.NewRunner("codex-cli-example", ag)
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
	var lastAssistantResponseID string
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("Error: %s (%s)\n", evt.Error.Message, evt.Error.Type)
			continue
		}
		printToolEvents(evt)
		printThreadState(evt)
		printAssistantEvent(evt, &lastAssistantResponseID)
	}
	return nil
}

func printAssistantEvent(evt *event.Event, lastAssistantResponseID *string) {
	if evt.Response == nil || len(evt.Choices) == 0 || evt.IsToolCallResponse() || evt.IsToolResultResponse() {
		return
	}
	content := assistantText(evt)
	if content == "" {
		return
	}
	if evt.IsFinalResponse() {
		if evt.Response.ID != "" && lastAssistantResponseID != nil && evt.Response.ID == *lastAssistantResponseID {
			return
		}
		fmt.Printf("Assistant: %s\n", content)
		return
	}
	if evt.Object != model.ObjectTypeChatCompletionChunk || evt.Done || !evt.IsPartial {
		return
	}
	if lastAssistantResponseID != nil {
		*lastAssistantResponseID = evt.Response.ID
	}
	fmt.Printf("Assistant: %s\n", content)
}

func assistantText(evt *event.Event) string {
	content := strings.TrimSpace(evt.Choices[0].Message.Content)
	if content != "" {
		return content
	}
	return strings.TrimSpace(evt.Choices[0].Delta.Content)
}

func printToolEvents(evt *event.Event) {
	if evt.IsToolCallResponse() && len(evt.Choices) > 0 {
		for _, call := range evt.Choices[0].Message.ToolCalls {
			args := strings.TrimSpace(string(call.Function.Arguments))
			if args == "" {
				fmt.Printf("🔧 Tool call: %s\n", call.Function.Name)
				continue
			}
			fmt.Printf("🔧 Tool call: %s args=%s\n", call.Function.Name, args)
		}
	}
	if evt.IsToolResultResponse() && len(evt.Choices) > 0 {
		name := strings.TrimSpace(evt.Choices[0].Message.ToolName)
		content := strings.TrimSpace(evt.Choices[0].Message.Content)
		if name == "" {
			name = "unknown"
		}
		if content == "" {
			fmt.Printf("✅ Tool result: %s\n", name)
			return
		}
		fmt.Printf("✅ Tool result: %s content=%s\n", name, content)
	}
}

func printThreadState(evt *event.Event) {
	if evt.StateDelta == nil {
		return
	}
	threadID := strings.TrimSpace(string(evt.StateDelta[codex.StateKeyThreadID]))
	if threadID == "" {
		return
	}
	fmt.Printf("Thread state: %s=%s\n", codex.StateKeyThreadID, threadID)
}
