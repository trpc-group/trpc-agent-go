//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the error message plugin.
//
// The example wires a minimal agent that always emits a raw error event (the
// same shape llmflow produces for StopError) and runs two Runners:
//
//   - one without the plugin, so Runner applies its default fallback message
//   - one with the plugin, so a custom, user-facing message is used instead
//
// The example does not call any model backend, so no API key is required.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/errormessage"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName   = "error-message-demo"
	agentName = "stopping-agent"
	userID    = "demo-user"
)

func main() {
	fmt.Println("Error Message Plugin Demo")
	fmt.Println(strings.Repeat("=", 72))

	if err := runWithoutPlugin(); err != nil {
		fmt.Printf("default run failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(strings.Repeat("-", 72))

	if err := runWithPlugin(); err != nil {
		fmt.Printf("plugin run failed: %v\n", err)
		os.Exit(1)
	}
}

func runWithoutPlugin() error {
	fmt.Println("[1] Runner without errormessage plugin (default fallback).")
	svc := sessioninmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		newStopAgent(),
		runner.WithSessionService(svc),
	)
	defer r.Close()

	sessionID := "default"
	if err := drainRun(r, sessionID); err != nil {
		return err
	}
	return printPersistedErrorEvent(svc, sessionID)
}

func runWithPlugin() error {
	fmt.Println("[2] Runner with errormessage plugin (customised content).")
	svc := sessioninmemory.NewSessionService()

	// Example: produce a friendly message for stop_agent_error, and a generic
	// one for any other error type. The structured Response.Error is left
	// intact, so debugging and downstream consumers keep the raw reason.
	rewriter := errormessage.New(
		errormessage.WithResolver(func(
			_ context.Context,
			_ *agent.Invocation,
			e *event.Event,
		) (string, bool) {
			if e == nil || e.Response == nil || e.Response.Error == nil {
				return "", false
			}
			if e.Response.Error.Type == agent.ErrorTypeStopAgentError {
				return "本次执行已按策略停止，请稍后再试。", true
			}
			return "执行失败，请稍后重试。", true
		}),
	)

	r := runner.NewRunner(
		appName,
		newStopAgent(),
		runner.WithSessionService(svc),
		runner.WithPlugins(rewriter),
	)
	defer r.Close()

	sessionID := "rewritten"
	if err := drainRun(r, sessionID); err != nil {
		return err
	}
	return printPersistedErrorEvent(svc, sessionID)
}

func drainRun(r runner.Runner, sessionID string) error {
	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("trigger a stop"),
	)
	if err != nil {
		return err
	}
	for range ch {
		// Drain; persistence happens in the runner event loop and we only
		// care about what is stored on the session.
	}
	return nil
}

func printPersistedErrorEvent(
	svc session.Service,
	sessionID string,
) error {
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}
	for _, evt := range sess.Events {
		if evt.Response == nil || evt.Response.Error == nil {
			continue
		}
		content := ""
		if len(evt.Response.Choices) > 0 {
			content = evt.Response.Choices[0].Message.Content
		}
		fmt.Println("  Persisted error event:")
		fmt.Printf("    Response.Error.Type : %s\n", evt.Response.Error.Type)
		fmt.Printf("    Response.Error.Msg  : %s\n", evt.Response.Error.Message)
		fmt.Printf("    Visible Content     : %s\n", content)
		return nil
	}
	return fmt.Errorf("no error event persisted in session %q", sessionID)
}
