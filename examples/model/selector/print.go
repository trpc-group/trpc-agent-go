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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func printBanner(cfg appConfig) {
	fmt.Println("Model Selector Example")
	fmt.Printf("Tool-call model: %s\n", cfg.toolCallModelName)
	fmt.Printf("Final-answer model: %s\n", cfg.finalModelName)
	fmt.Println(strings.Repeat("=", 50))
}

func printEvents(eventChan <-chan *event.Event) error {
	var output strings.Builder
	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("agent event error: %s", evt.Error.Message)
		}
		if printToolCalls(evt) {
			continue
		}
		if printToolResponses(evt) {
			continue
		}
		if len(evt.Choices) > 0 {
			content := extractContent(evt.Choices[0])
			if content != "" {
				if output.Len() == 0 {
					fmt.Print("Assistant: ")
				}
				fmt.Print(content)
				output.WriteString(content)
			}
		}
		if evt.Done {
			break
		}
	}
	if output.Len() == 0 {
		fmt.Println("Assistant: (empty response)")
		return nil
	}
	fmt.Println()
	return nil
}

func printToolCalls(evt *event.Event) bool {
	if len(evt.Choices) == 0 {
		return false
	}
	toolCalls := evt.Choices[0].Message.ToolCalls
	if len(toolCalls) == 0 {
		return false
	}
	fmt.Println()
	for _, toolCall := range toolCalls {
		fmt.Printf("Tool call: %s\n", toolCall.Function.Name)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("Args: %s\n", string(toolCall.Function.Arguments))
		}
	}
	return true
}

func printToolResponses(evt *event.Event) bool {
	if len(evt.Choices) == 0 {
		return false
	}
	printed := false
	for _, choice := range evt.Choices {
		if choice.Message.Role != model.RoleTool || choice.Message.ToolID == "" {
			continue
		}
		fmt.Printf("\nTool result: %s\n", strings.TrimSpace(choice.Message.Content))
		printed = true
	}
	return printed
}

func extractContent(choice model.Choice) string {
	if choice.Delta.Content != "" {
		return choice.Delta.Content
	}
	return choice.Message.Content
}
