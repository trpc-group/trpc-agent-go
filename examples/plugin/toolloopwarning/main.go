//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the tool-loop warning plugin with a deterministic
// model, a real LLMAgent tool loop, and a Runner-backed session.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolloopwarning"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName     = "tool-loop-warning-example"
	userID      = "example-user"
	sessionID   = "tool-loop-warning-session"
	searchTool  = "search_docs"
	loopWarning = "The same tool loop repeated. Stop calling it and use another approach."
)

type searchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tool-loop warning example failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	modelInstance := &scriptedModel{}
	toolCalls := 0
	searchDocs := function.NewFunctionTool(
		func(_ context.Context, input searchInput) (string, error) {
			toolCalls++
			fmt.Printf(
				"tool %s: query=%q limit=%d\n",
				searchTool,
				input.Query,
				input.Limit,
			)
			return "plugin/toolloopwarning contains the request-local loop warning plugin", nil
		},
		function.WithName(searchTool),
		function.WithDescription("Search the tRPC-Agent-Go documentation."),
	)

	agentInstance := llmagent.New(
		"tool-loop-warning-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithTools([]tool.Tool{searchDocs}),
	)
	sessionService := sessioninmemory.NewSessionService()
	runnerInstance := runner.NewRunner(
		appName,
		agentInstance,
		runner.WithSessionService(sessionService),
		runner.WithPlugins(toolloopwarning.New(
			toolloopwarning.WithWarningMessage(loopWarning),
		)),
	)
	defer runnerInstance.Close()

	events, err := runnerInstance.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage("Find the tool-loop warning plugin documentation."),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	finalResponse, err := collectFinalResponse(events)
	if err != nil {
		return err
	}

	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return errors.New("session not found")
	}

	fmt.Printf("assistant: %s\n", finalResponse)
	fmt.Printf("tool calls: %d\n", toolCalls)
	fmt.Printf(
		"session contains loop warning: %t\n",
		sessionContainsWarning(sess),
	)
	return nil
}

func collectFinalResponse(events <-chan *event.Event) (string, error) {
	finalResponse := ""
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return "", errors.New(evt.Error.Message)
		}
		if evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			return "", errors.New(evt.Response.Error.Message)
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				choice.Message.Content != "" {
				finalResponse = choice.Message.Content
			}
		}
	}
	if finalResponse == "" {
		return "", errors.New("final assistant response not found")
	}
	return finalResponse, nil
}

func sessionContainsWarning(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	for _, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Content == loopWarning ||
				choice.Delta.Content == loopWarning {
				return true
			}
		}
	}
	return false
}
