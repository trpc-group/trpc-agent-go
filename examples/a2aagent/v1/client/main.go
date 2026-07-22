//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-turn sessions through an A2A v1 agent.
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

	a2aagent "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName = "a2a-v1-client"
	userID  = "example-user"
)

var (
	serverURL = flag.String(
		"url",
		"http://127.0.0.1:8888",
		"A2A server URL",
	)
	initialSessionID = flag.String(
		"session",
		"",
		"Initial session ID (generated when empty)",
	)
)

func main() {
	flag.Parse()

	remoteAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(*serverURL),
	)
	if err != nil {
		log.Fatalf("connect A2A agent: %v", err)
	}
	card := remoteAgent.GetAgentCard()
	fmt.Printf("Connected to %s at %s\n", card.Name, card.PrimaryURL())

	agentRunner := runner.NewRunner(
		appName,
		remoteAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer agentRunner.Close()

	sessionID := strings.TrimSpace(*initialSessionID)
	if sessionID == "" {
		sessionID = newSessionID()
	}
	if err := chat(agentRunner, sessionID); err != nil {
		log.Fatalf("chat: %v", err)
	}
}

func chat(agentRunner runner.Runner, sessionID string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println("Commands: /new [id], /use <id>, /exit")

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		lowerInput := strings.ToLower(input)
		switch {
		case lowerInput == "/exit":
			return nil
		case strings.HasPrefix(lowerInput, "/new"):
			requestedID := strings.TrimSpace(input[len("/new"):])
			if requestedID == "" {
				requestedID = newSessionID()
			}
			sessionID = requestedID
			fmt.Printf("Started session: %s\n", sessionID)
			continue
		case strings.HasPrefix(lowerInput, "/use"):
			requestedID := strings.TrimSpace(input[len("/use"):])
			if requestedID == "" {
				fmt.Println("Usage: /use <session-id>")
				continue
			}
			sessionID = requestedID
			fmt.Printf("Using session: %s\n", sessionID)
			continue
		}

		events, err := agentRunner.Run(
			context.Background(),
			userID,
			sessionID,
			model.NewUserMessage(input),
		)
		if err != nil {
			return fmt.Errorf("run A2A agent: %w", err)
		}
		if err := printResponse(events); err != nil {
			return err
		}
	}
}

func printResponse(events <-chan *event.Event) error {
	printed := false
	sawPartial := false
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("remote agent: %s", evt.Error.Message)
		}
		if evt.Response == nil {
			continue
		}

		isPartial := evt.Response.IsPartial
		if isPartial {
			sawPartial = true
		}
		for _, choice := range evt.Response.Choices {
			var content string
			if isPartial {
				content = choice.Delta.Content
			} else if !sawPartial {
				content = choice.Message.Content
			}
			if content == "" {
				continue
			}
			if !printed {
				fmt.Print("Assistant: ")
				printed = true
			}
			fmt.Print(content)
		}
		if evt.IsFinalResponse() {
			break
		}
	}
	if printed {
		fmt.Println()
	}
	return nil
}

func newSessionID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}
