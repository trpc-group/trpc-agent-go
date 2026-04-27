//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func printTools(tools map[string]tool.Tool) {
	if len(tools) == 0 {
		fmt.Println("tools: <none>")
		return
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("tools: %s\n", strings.Join(names, ", "))
}

func printMessages(messages []model.Message, limit int) {
	if len(messages) == 0 {
		fmt.Println("messages: <none>")
		return
	}
	fmt.Printf("messages: %d\n", len(messages))
	for i, msg := range messages {
		fmt.Printf("[%02d] role=%s tool_id=%s tool_name=%s content=%q\n", i, msg.Role, emptyAsDash(msg.ToolID), emptyAsDash(msg.ToolName), clip(msg.Content, limit))
		if len(msg.ToolCalls) > 0 {
			printToolCalls("     tool_call", msg.ToolCalls, limit)
		}
		if msg.ReasoningContent != "" {
			fmt.Printf("     reasoning=%q\n", clip(msg.ReasoningContent, limit))
		}
		if len(msg.ContentParts) > 0 {
			fmt.Printf("     content_parts=%d\n", len(msg.ContentParts))
		}
	}
}

func printToolCalls(prefix string, calls []model.ToolCall, limit int) {
	for i, call := range calls {
		fmt.Printf("%s[%d] id=%s name=%s args=%s\n", prefix, i, emptyAsDash(call.ID), call.Function.Name, prettyJSONBytes(call.Function.Arguments, limit))
	}
}

func printEvents(events <-chan *event.Event, limit int) error {
	fmt.Println("\n=== Events ===")
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("[event] author=%s error=%s\n", ev.Author, ev.Error.Message)
			continue
		}
		if ev.Response == nil {
			continue
		}
		if ev.Object == model.ObjectTypeTransfer {
			fmt.Printf("[event] author=%s transfer=%q\n", ev.Author, eventText(ev, limit))
			continue
		}
		if ev.Response.IsToolCallResponse() {
			fmt.Printf("[event] author=%s tool_call\n", ev.Author)
			printEventToolCalls(ev, limit)
			continue
		}
		if ev.IsToolResultResponse() {
			fmt.Printf("[event] author=%s tool_result=%q\n", ev.Author, eventText(ev, limit))
			continue
		}
		text := eventText(ev, limit)
		if text != "" {
			fmt.Printf("[event] author=%s text=%q\n", ev.Author, text)
		}
	}
	return nil
}

func printEventToolCalls(ev *event.Event, limit int) {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return
	}
	calls := ev.Response.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		calls = ev.Response.Choices[0].Delta.ToolCalls
	}
	printToolCalls("  ", calls, limit)
}

func eventText(ev *event.Event, limit int) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	choice := ev.Response.Choices[0]
	if choice.Message.Content != "" {
		return clip(choice.Message.Content, limit)
	}
	return clip(choice.Delta.Content, limit)
}
