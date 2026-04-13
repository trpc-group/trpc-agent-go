//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func printBanner(modelName string, location string, failures int) {
	fmt.Println("🔁 Graph Tool-Call Retry Example")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Location: %s\n", location)
	fmt.Printf("Transient failures before success: %d\n", failures)
	fmt.Println(strings.Repeat("=", 60))
}

func printScenarioHeader(name string) {
	fmt.Printf("== %s ==\n", name)
}

func printToolAttempt(attempt int, location string) {
	fmt.Printf("tool attempt %d for %s\n", attempt, location)
}

func printScenarioResult(
	name string,
	attempts int,
	answer string,
	runErr error,
	expectSuccess bool,
) error {
	switch {
	case expectSuccess && runErr != nil:
		return fmt.Errorf("%s failed unexpectedly: %w", name, runErr)
	case !expectSuccess && runErr == nil:
		return fmt.Errorf("%s succeeded unexpectedly: %s", name, answer)
	case runErr != nil:
		fmt.Printf("result: failed after %d attempt(s): %v\n", attempts, runErr)
	default:
		fmt.Printf("result: succeeded after %d attempt(s)\n", attempts)
		fmt.Printf("answer: %s\n", answer)
	}
	return nil
}

func collectScenarioResult(events <-chan *event.Event) (string, error) {
	var (
		answer string
		runErr error
	)
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			runErr = errors.New(evt.Error.Message)
			continue
		}
		if evt.Response == nil {
			continue
		}
		if evt.IsToolCallResponse() {
			printToolCalls(evt)
			continue
		}
		if !evt.IsFinalResponse() || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
			answer = choice.Message.Content
		}
	}
	return answer, runErr
}

func printToolCalls(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf(
				"llm tool call: %s args=%s\n",
				toolCall.Function.Name,
				compactJSON(toolCall.Function.Arguments),
			)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			fmt.Printf(
				"llm tool call: %s args=%s\n",
				toolCall.Function.Name,
				compactJSON(toolCall.Function.Arguments),
			)
		}
	}
}

func compactJSON(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "{}"
	}
	return text
}
