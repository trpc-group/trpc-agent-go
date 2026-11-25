//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive Agent Client Protocol client.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent/acpagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const appName = "acp-client-example"

type repeatedArgs []string

func (a *repeatedArgs) String() string {
	return strings.Join(*a, " ")
}

func (a *repeatedArgs) Set(value string) error {
	*a = append(*a, value)
	return nil
}

var (
	command     = flag.String("command", "codex-acp", "ACP agent executable")
	workDir     = flag.String("work-dir", ".", "ACP agent working directory")
	userID      = flag.String("user-id", "demo-user", "Framework user ID")
	sessionID   = flag.String("session-id", uuid.NewString(), "Framework session ID")
	idleTimeout = flag.Duration(
		"idle-timeout",
		5*time.Minute,
		"Recycle the shared ACP process after this idle duration; zero disables",
	)
	args repeatedArgs
)

func init() {
	flag.Var(&args, "arg", "ACP agent argument; repeat for multiple arguments")
}

func main() {
	flag.Parse()
	absoluteWorkDir, err := filepath.Abs(*workDir)
	if err != nil {
		log.Fatalf("resolve working directory: %v", err)
	}

	acpAgent, err := acpagent.New(
		"acp-client",
		acpagent.WithDescription("ACP client example"),
		acpagent.WithCommand(*command),
		acpagent.WithArgs([]string(args)...),
		acpagent.WithWorkDir(absoluteWorkDir),
		acpagent.WithIdleTimeout(*idleTimeout),
		acpagent.WithStderrWriter(os.Stderr),
		acpagent.WithAuthMethodSelectorFunc(selectAgentAuth),
	)
	if err != nil {
		log.Fatalf("create ACP agent: %v", err)
	}
	defer func() {
		if err := acpAgent.Close(); err != nil {
			log.Printf("close ACP agent: %v", err)
		}
	}()

	r := runner.NewRunner(appName, acpAgent)
	defer r.Close()

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Type a prompt, /close-session to close the ACP session, or /exit to quit.")
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			return
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit":
			return
		case "/close-session":
			if err := acpAgent.CloseSession(ctx, session.Key{
				AppName:   appName,
				UserID:    *userID,
				SessionID: *sessionID,
			}); err != nil {
				fmt.Printf("CloseSession failed: %v\n", err)
			} else {
				fmt.Println("ACP session closed.")
			}
			continue
		}
		if err := runOnce(ctx, r, *userID, *sessionID, prompt); err != nil {
			fmt.Printf("Run failed: %v\n", err)
		}
	}
}

func selectAgentAuth(
	_ context.Context,
	challenge acpagent.AuthChallenge,
) (acpagent.AuthSelection, error) {
	for _, method := range challenge.Methods {
		if method.Type == acpagent.AuthMethodTypeAgent {
			return acpagent.AuthSelection{MethodID: method.ID}, nil
		}
	}
	return acpagent.AuthSelection{}, fmt.Errorf(
		"no agent authentication method is available for %s",
		challenge.Operation,
	)
}

func runOnce(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	prompt string,
) error {
	events, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return err
	}
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("Error: %s (%s)\n", evt.Error.Message, evt.Error.Type)
			continue
		}
		printToolCall(evt)
		if evt.IsFinalResponse() && len(evt.Choices) > 0 {
			fmt.Printf("Assistant: %s\n", strings.TrimSpace(evt.Choices[0].Message.Content))
		}
	}
	return nil
}

func printToolCall(evt *event.Event) {
	if !evt.IsToolCallResponse() || len(evt.Choices) == 0 {
		return
	}
	for _, call := range evt.Choices[0].Message.ToolCalls {
		arguments := strings.TrimSpace(string(call.Function.Arguments))
		if arguments == "" {
			fmt.Printf("Tool call: %s\n", call.Function.Name)
			continue
		}
		fmt.Printf("Tool call: %s args=%s\n", call.Function.Name, arguments)
	}
}
