//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

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
	"trpc.group/trpc-go/trpc-agent-go/event"
	memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultModelName  = "deepseek-v4-flash"
	defaultGatewayURL = "http://127.0.0.1:8420"
)

var (
	modelName = flag.String("model", defaultModelName, "Chat model name")
	appName   = flag.String(
		"app",
		"tencentdb-memory-demo",
		"Application name used for session ownership",
	)
	userID = flag.String(
		"user",
		"demo-user",
		"User ID used for session ownership",
	)
	sessionID = flag.String(
		"session",
		"",
		"Session ID (default: generated from timestamp)",
	)
	gatewayURL = flag.String(
		"gateway",
		envOrDefault("TENCENTDB_AGENT_MEMORY_GATEWAY", defaultGatewayURL),
		"TencentDB Agent Memory gateway URL",
	)
	gatewayTimeout = flag.Duration(
		"gateway-timeout",
		60*time.Second,
		"Timeout for TencentDB Agent Memory gateway requests",
	)
	waitBeforeRecall = flag.Duration(
		"turn-wait",
		0,
		"Delay after each user turn to wait for gateway capture/extraction",
	)
	endSession = flag.Bool(
		"end-session",
		false,
		"Call TencentDB Agent Memory /session/end before exit",
	)
)

func main() {
	flag.Parse()
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	sid := *sessionID
	if sid == "" {
		sid = fmt.Sprintf("tencentdb-%d", time.Now().Unix())
	}

	memSvc, err := memorytencentdb.NewService(
		memorytencentdb.WithGatewayURL(*gatewayURL),
		memorytencentdb.WithTimeout(*gatewayTimeout),
		memorytencentdb.WithIngestQueueSize(8),
		memorytencentdb.WithIngestJobTimeout(30*time.Second),
	)
	if err != nil {
		log.Fatalf("create TencentDB Agent Memory service: %v", err)
	}
	defer memSvc.Close()

	ctx := context.Background()
	health, err := memSvc.Health(ctx)
	if err != nil {
		log.Fatalf("TencentDB Agent Memory gateway is not ready: %v", err)
	}

	chatAgent := llmagent.New(
		"tencentdb-memory-demo-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("A concise assistant with TencentDB Agent Memory integration."),
		llmagent.WithTools(memSvc.Tools()),
	)

	sessionSvc := sessioninmemory.NewSessionService()
	r := runner.NewRunner(
		*appName,
		chatAgent,
		runner.WithSessionService(sessionSvc),
		runner.WithSessionIngestor(memSvc),
		runner.WithPlugins(memSvc.Plugin()),
	)
	defer r.Close()

	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Gateway: %s (status=%s version=%s)\n", *gatewayURL, health.Status, health.Version)
	fmt.Printf("App: %s\nUser: %s\nSession: %s\n", *appName, *userID, sid)
	fmt.Println(strings.Repeat("=", 60))

	chat := &memoryChat{
		runner:     r,
		sessionSvc: sessionSvc,
		memSvc:     memSvc,
		userID:     *userID,
		sessionID:  sid,
	}
	if err := chat.start(ctx); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

type memoryChat struct {
	runner     runner.Runner
	sessionSvc session.Service
	memSvc     *memorytencentdb.Service
	userID     string
	sessionID  string
}

func (c *memoryChat) start(ctx context.Context) error {
	if *endSession {
		defer func() {
			if err := c.endCurrentSession(ctx); err != nil {
				fmt.Printf("End session failed: %v\n", err)
			} else {
				fmt.Println("Session flushed through TencentDB Agent Memory gateway.")
			}
		}()
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Special commands:")
	fmt.Println("  /new      - flush current session and start a new session for the same user")
	fmt.Println("  /session  - show current session")
	fmt.Println("  /end      - call TencentDB Agent Memory /session/end for current session")
	fmt.Println("  /exit     - end the conversation")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "/exit":
			fmt.Println("Goodbye!")
			return nil
		case "/new":
			if err := c.startNewSession(ctx); err != nil {
				fmt.Printf("Start new session failed: %v\n\n", err)
			}
			continue
		case "/session":
			fmt.Printf("Current session: %s\n\n", c.sessionID)
			continue
		case "/end":
			if err := c.endCurrentSession(ctx); err != nil {
				fmt.Printf("End session failed: %v\n\n", err)
			} else {
				fmt.Println("Session flushed through TencentDB Agent Memory gateway.")
				fmt.Println()
			}
			continue
		}

		if err := c.processMessage(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *memoryChat) processMessage(ctx context.Context, input string) error {
	result, err := runOnce(ctx, c.runner, c.userID, c.sessionID, model.NewUserMessage(input))
	if err != nil {
		return err
	}
	printRunResult(result)
	if *waitBeforeRecall > 0 {
		fmt.Printf("Waiting %s for gateway capture/extraction...\n", *waitBeforeRecall)
		time.Sleep(*waitBeforeRecall)
	}
	return nil
}

func (c *memoryChat) startNewSession(ctx context.Context) error {
	oldSessionID := c.sessionID
	if err := c.endCurrentSession(ctx); err != nil {
		return err
	}
	c.sessionID = fmt.Sprintf("tencentdb-%d", time.Now().UnixNano())
	fmt.Println("Started new session.")
	fmt.Printf("  Previous: %s\n", oldSessionID)
	fmt.Printf("  Current:  %s\n", c.sessionID)
	fmt.Println("  Long-term memories are preserved for the same user.")
	fmt.Println()
	return nil
}

func (c *memoryChat) endCurrentSession(ctx context.Context) error {
	sess, err := c.sessionSvc.GetSession(ctx, sessionKey(c.userID, c.sessionID))
	if err != nil {
		return fmt.Errorf("lookup session: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", c.sessionID)
	}
	return c.memSvc.EndSession(ctx, sess)
}

type runResult struct {
	toolCalls []string
	reply     string
}

func runOnce(ctx context.Context, r runner.Runner, userID, sessionID string, msg model.Message) (*runResult, error) {
	ch, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return nil, err
	}
	out := &runResult{}
	seen := make(map[string]struct{})
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return nil, fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		collectResponse(out, seen, evt)
	}
	return out, nil
}

func collectResponse(out *runResult, seen map[string]struct{}, evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, tc := range choice.Message.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out.toolCalls = append(out.toolCalls, name)
		}
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			out.reply = text
		}
	}
}

func printRunResult(result *runResult) {
	if len(result.toolCalls) > 0 {
		fmt.Printf("Tool calls: %s\n", strings.Join(result.toolCalls, ", "))
	} else {
		fmt.Println("Tool calls: <none>")
	}
	if reply := strings.TrimSpace(result.reply); reply != "" {
		fmt.Printf("Assistant: %s\n", reply)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func sessionKey(userID, sessionID string) session.Key {
	return session.Key{
		AppName:   *appName,
		UserID:    userID,
		SessionID: sessionID,
	}
}
