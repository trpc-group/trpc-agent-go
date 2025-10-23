//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main shows how to filter only the model's final reasoning
// using the built-in event tags. It prints streaming output but only
// displays reasoning when the tag equals event.TagReasoningFinal.
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-reasoner", "Name of the model to use")
)

func main() {
	flag.Parse()

	// Create agent with streaming + reasoning enabled.
	m := openai.New(*modelName)
	gen := model.GenerationConfig{
		Stream:          true,
		ThinkingEnabled: boolPtr(true),
		ThinkingTokens:  intPtr(2048),
		MaxTokens:       intPtr(1024),
		Temperature:     floatPtr(0.7),
	}
	a := llmagent.New(
		"reasoning-tags-demo",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithDescription("Demonstrates filtering final reasoning via tags."),
	)

	r := runner.NewRunner("reasoning-tags-demo", a, runner.WithSessionService(inmemory.NewSessionService()))

	ctx := context.Background()
	evtCh, err := r.Run(ctx, "user", fmt.Sprintf("sess-%d", time.Now().Unix()), model.NewUserMessage(
		"Please solve a task that requires a tool in two steps: First, check the weather in Beijing (using a tool). Second, provide a brief suggestion.",
	))
	if err != nil {
		panic(err)
	}

	for ev := range evtCh {
		if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}
		ch := ev.Response.Choices[0]
		// Stream deltas, but only when tag marks final reasoning.
		if ev.Tag == event.TagReasoningFinal && ch.Delta.ReasoningContent != "" {
			fmt.Print(ch.Delta.ReasoningContent)
		}
		// Always show the visible content normally.
		if ch.Delta.Content != "" {
			fmt.Print(ch.Delta.Content)
		}
		if ev.IsRunnerCompletion() {
			fmt.Println()
			break
		}
	}
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool        { return &v }
