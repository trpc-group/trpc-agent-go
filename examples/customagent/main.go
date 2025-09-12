//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates implementing a custom Agent by hand without Graph.
// It performs a simple intent classification and branches the logic:
// - chitchat: reply conversationally
// - task: provide a short actionable plan
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
    modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
    query     = flag.String("q", "今天天气怎么样，随便聊聊~", "User input text")
)

func main() {
    flag.Parse()

    fmt.Printf("🚀 Custom Agent (intent-branching)\n")
    fmt.Printf("Model: %s\n", *modelName)
    fmt.Printf("Query: %s\n", *query)
    fmt.Println(strings.Repeat("=", 50))

    // Build model and custom agent.
    m := openai.New(*modelName)
    ag := NewSimpleIntentAgent(
        "biz-agent",
        "A custom agent demonstrating business flow branching by intent",
        m,
    )

    // Use Runner for session + event handling.
    r := runner.NewRunner("customagent-app", ag)
    ctx := context.Background()

    ch, err := r.Run(ctx, "user-001", "session-001", model.NewUserMessage(*query))
    if err != nil {
        log.Fatalf("run failed: %v", err)
    }

    // Stream events.
    fmt.Print("🤖 Assistant: ")
    for evt := range ch {
        if evt.Error != nil {
            fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
            continue
        }
        printContent(evt)
        if evt.Done && !isToolLike(evt) {
            fmt.Println()
        }
    }
}

func printContent(evt *event.Event) {
    if evt.Response == nil || len(evt.Response.Choices) == 0 {
        return
    }
    c := evt.Response.Choices[0]
    if c.Delta.Content != "" {
        fmt.Print(c.Delta.Content)
    }
    if c.Message.Content != "" && !evt.Response.IsPartial {
        fmt.Print(c.Message.Content)
    }
}

func isToolLike(evt *event.Event) bool {
    if evt.Response == nil {
        return false
    }
    // Minimal check: tool calls or tool role messages.
    if len(evt.Response.Choices) > 0 {
        ch := evt.Response.Choices[0]
        if len(ch.Message.ToolCalls) > 0 {
            return true
        }
        if ch.Message.Role == model.RoleTool {
            return true
        }
    }
    return false
}

