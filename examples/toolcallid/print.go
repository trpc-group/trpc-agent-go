//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func printBanner(modelName string, variant string, streaming bool, prompt string) {
	fmt.Println("🔗 Tool Call ID Plugin Demo")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Variant: %s\n", variant)
	fmt.Printf("Streaming: %t\n", streaming)
	fmt.Printf("Prompt: %s\n", prompt)
	fmt.Println(strings.Repeat("=", 72))
}

func printEvents(eventCh <-chan *event.Event) error {
	streamStarted := false
	for evt := range eventCh {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		if evt.IsToolCallResponse() {
			printToolCalls(evt)
		}
		if evt.IsToolResultResponse() {
			printToolResults(evt)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Delta.Content != "" {
				if !streamStarted {
					fmt.Print("[assistant] ")
					streamStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
			if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" && !streamStarted {
				fmt.Printf("[assistant] %s\n", choice.Message.Content)
			}
		}
	}
	if streamStarted {
		fmt.Println()
	}
	return nil
}

func printToolCalls(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf(
				"[event] tool_call tool=%s call_id=%s args=%s\n",
				toolCall.Function.Name,
				toolCall.ID,
				compactJSON(toolCall.Function.Arguments),
			)
		}
	}
}

func printToolResults(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role != model.RoleTool || choice.Message.ToolID == "" {
			continue
		}
		fmt.Printf(
			"[event] tool_result call_id=%s result=%s\n",
			choice.Message.ToolID,
			preview(choice.Message.Content, 160),
		)
	}
}

func printCalculatorExecution(callID string, args calculatorArgs) {
	fmt.Printf(
		"[tool] tool=%s call_id=%s operation=%s a=%v b=%v\n",
		toolNameCalc,
		callID,
		args.Operation,
		args.A,
		args.B,
	)
}

func compactJSON(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "{}"
	}
	return text
}

func preview(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}
