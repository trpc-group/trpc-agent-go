//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

func printBanner(modelName string, location string, failures int) {
	fmt.Println("🔁 LLMAgent Tool-Call Retry Example")
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
	toolResponse string,
	runErr error,
	expectSuccess bool,
) error {
	if toolResponse == "" && runErr == nil {
		return fmt.Errorf("%s produced no tool response", name)
	}
	switch {
	case expectSuccess && runErr != nil:
		return fmt.Errorf("%s failed unexpectedly: %w", name, runErr)
	case expectSuccess && isToolErrorMessage(toolResponse):
		return fmt.Errorf("%s failed unexpectedly: %s", name, toolResponse)
	case !expectSuccess && runErr == nil && !isToolErrorMessage(toolResponse):
		return fmt.Errorf("%s succeeded unexpectedly: %s", name, normalizeToolResponse(toolResponse))
	case runErr != nil:
		fmt.Printf("result: failed after %d attempt(s): %v\n", attempts, runErr)
	default:
		if isToolErrorMessage(toolResponse) {
			fmt.Printf("result: failed after %d attempt(s): %s\n", attempts, toolResponse)
			return nil
		}
		fmt.Printf("result: succeeded after %d attempt(s)\n", attempts)
		fmt.Printf("tool response: %s\n", normalizeToolResponse(toolResponse))
	}
	return nil
}

func collectScenarioResult(
	events <-chan *event.Event,
	stop func(),
) (string, error) {
	var (
		toolResponse string
		runErr       error
		stopped      bool
	)
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			if stopped && strings.Contains(evt.Error.Message, context.Canceled.Error()) {
				continue
			}
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
		if evt.Response.IsToolResultResponse() {
			toolResponse = firstToolResponseContent(evt)
			if stop != nil && !stopped {
				stop()
				stopped = true
			}
		}
	}
	if stopped && (errors.Is(runErr, context.Canceled) || strings.Contains(runErrString(runErr), context.Canceled.Error())) {
		runErr = nil
	}
	return toolResponse, runErr
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

func firstToolResponseContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	for _, choice := range evt.Response.Choices {
		if choice.Message.ToolID != "" && choice.Message.Content != "" {
			return choice.Message.Content
		}
		if choice.Delta.ToolID != "" && choice.Delta.Content != "" {
			return choice.Delta.Content
		}
	}
	return ""
}

func isToolErrorMessage(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "Error:")
}

func normalizeToolResponse(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "<empty>"
	}
	unquoted, err := strconv.Unquote(trimmed)
	if err == nil {
		return unquoted
	}
	return trimmed
}

func runErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func compactJSON(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "{}"
	}
	return text
}
