//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main calls a tRPC-Agent API server through the remote runner.
package main

import (
	"context"
	"fmt"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcagentrunner "trpc.group/trpc-go/trpc-agent-go/runner/trpcagent"
)

const (
	target    = "http://127.0.0.1:8080"
	appName   = "calculator"
	userID    = "alice"
	sessionID = "demo-session"
	prompt    = "Use the calculator to compute 12 * 7."
)

func main() {
	ctx := context.Background()
	r, err := trpcagentrunner.New(appName, trpcagentrunner.WithTarget(target))
	if err != nil {
		log.Fatalf("failed to create tRPC-Agent runner: %v", err)
	}
	defer r.Close()
	snapshot, err := r.Describe(ctx)
	if err != nil {
		log.Fatalf("failed to describe remote app: %v", err)
	}
	printStructure(snapshot)
	events, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(prompt),
	)
	if err != nil {
		log.Fatalf("remote run failed: %v", err)
	}
	if err := printEvents(events); err != nil {
		log.Fatalf("remote run event failed: %v", err)
	}
}

func printStructure(snapshot *astructure.Snapshot) {
	if snapshot == nil {
		fmt.Println("structure: empty")
		return
	}
	fmt.Printf("structure: id=%s entry=%s nodes=%d surfaces=%d\n", snapshot.StructureID, snapshot.EntryNodeID, len(snapshot.Nodes), len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		fmt.Printf("surface: id=%s node=%s type=%s\n", surface.SurfaceID, surface.NodeID, surface.Type)
	}
}

func printEvents(eventCh <-chan *event.Event) error {
	var final strings.Builder
	streamed := false
	for evt := range eventCh {
		if evt == nil || evt.Response == nil {
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
		appendAssistantContent(evt, &final, &streamed)
		if evt.IsRunnerCompletion() {
			break
		}
	}
	if final.Len() > 0 {
		fmt.Printf("assistant: %s\n", final.String())
	}
	return nil
}

func appendAssistantContent(evt *event.Event, final *strings.Builder, streamed *bool) {
	for _, choice := range evt.Choices {
		if choice.Delta.Content != "" {
			final.WriteString(choice.Delta.Content)
			*streamed = true
		}
		if !*streamed && choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
			final.WriteString(choice.Message.Content)
		}
	}
}

func printToolCalls(evt *event.Event) {
	for _, choice := range evt.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf("tool call: name=%s id=%s args=%s\n", toolCall.Function.Name, toolCall.ID, compactJSON(toolCall.Function.Arguments))
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			fmt.Printf("tool call: name=%s id=%s args=%s\n", toolCall.Function.Name, toolCall.ID, compactJSON(toolCall.Function.Arguments))
		}
	}
}

func printToolResults(evt *event.Event) {
	for _, choice := range evt.Choices {
		if choice.Message.Role == model.RoleTool {
			fmt.Printf("tool result: id=%s content=%s\n", choice.Message.ToolID, choice.Message.Content)
		}
		if choice.Delta.Role == model.RoleTool {
			fmt.Printf("tool result: id=%s content=%s\n", choice.Delta.ToolID, choice.Delta.Content)
		}
	}
}

func compactJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
