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
	"context"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func printRunHeader(
	mode llmagent.ToolActivationMode,
	lifetime llmagent.ToolActivationLifetime,
	sessionID string,
) {
	fmt.Println("Skill Tool Activation")
	fmt.Printf("Model: %s\n", *flagModel)
	fmt.Printf("Mode: %s\n", mode)
	fmt.Printf("Lifetime: %s\n", lifetime)
	fmt.Printf("Skills root: %s\n", *flagSkillsRoot)
	fmt.Printf("Docs root: %s\n", *flagDocsRoot)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println()
	fmt.Printf("User: %s\n\n", *flagPrompt)
}

func traceToolCallbacks(enabled bool) *model.Callbacks {
	if !enabled {
		return nil
	}
	var call int
	return model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		call++
		fmt.Printf("[before model #%d] visible tools:\n", call)
		for _, name := range requestToolNames(args.Request) {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println()
		return nil, nil
	})
}

func requestToolNames(req *model.Request) []string {
	if req == nil || len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printEvent(evt *event.Event) {
	if evt == nil {
		return
	}
	if evt.Error != nil {
		fmt.Printf("error: %s\n", evt.Error.Message)
		return
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	ch := evt.Response.Choices[0]
	if len(ch.Message.ToolCalls) > 0 {
		fmt.Println("tool calls:")
		for _, tc := range ch.Message.ToolCalls {
			fmt.Printf("  - %s args=%s\n",
				tc.Function.Name,
				string(tc.Function.Arguments),
			)
		}
		return
	}
	if ch.Message.Role == model.RoleTool && ch.Message.Content != "" {
		fmt.Printf("tool result: %s\n", compactText(ch.Message.Content))
		return
	}
	if delta := strings.TrimSpace(ch.Delta.Content); delta != "" {
		fmt.Print(delta)
		return
	}
	if content := strings.TrimSpace(ch.Message.Content); content != "" {
		fmt.Printf("assistant: %s\n", content)
	}
}

func compactText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	const max = 220
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
