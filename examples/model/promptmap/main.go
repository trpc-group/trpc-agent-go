//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates model-specific prompt mapping for LLMAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName   = "model-promptmap"
	agentName = "promptmap-agent"
	userID    = "user"

	defaultGlobalInstruction = "System: You are a helpful assistant."
	defaultInstruction       = "Start every answer with \"DEFAULT:\"."

	defaultModelA = "gpt-4o-mini"
	defaultModelB = "gpt-4o"

	sessionA = "session-model-a"
	sessionB = "session-model-b"

	userMessageText = "Say hello in one sentence."
)

func main() {
	modelA := flag.String("a", defaultModelA, "Model A name")
	modelB := flag.String("b", defaultModelB, "Model B name")
	flag.Parse()

	ctx := context.Background()

	models := map[string]model.Model{
		*modelA: openai.New(*modelA),
		*modelB: openai.New(*modelB),
	}

	defaultModel, ok := models[*modelA]
	if !ok {
		fmt.Printf("Model %q not found\n", *modelA)
		return
	}

	agt := llmagent.New(
		agentName,
		llmagent.WithModels(models),
		llmagent.WithModel(defaultModel),
		llmagent.WithGlobalInstruction(defaultGlobalInstruction),
		llmagent.WithInstruction(defaultInstruction),
		llmagent.WithModelGlobalInstructions(map[string]string{
			*modelA: "System: You are in MODEL_A mode.",
			*modelB: "System: You are in MODEL_B mode.",
		}),
		llmagent.WithModelInstructions(map[string]string{
			*modelA: "Start every answer with \"MODEL_A:\".",
			*modelB: "Start every answer with \"MODEL_B:\".",
		}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: false,
		}),
	)

	r := runner.NewRunner(appName, agt)
	defer r.Close()

	fmt.Printf("Model A: %s\n", *modelA)
	fmt.Printf("Model B: %s\n\n", *modelB)

	msg := model.NewUserMessage(userMessageText)

	fmt.Printf("Run 1 (default model): %s\n", *modelA)
	if err := runOnce(ctx, r, sessionA, msg); err != nil {
		fmt.Printf("Run 1 error: %v\n", err)
		return
	}

	fmt.Printf("\nRun 2 (per-request model): %s\n", *modelB)
	if err := runOnce(
		ctx,
		r,
		sessionB,
		msg,
		agent.WithModelName(*modelB),
	); err != nil {
		fmt.Printf("Run 2 error: %v\n", err)
		return
	}
}

func runOnce(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	msg model.Message,
	opts ...agent.RunOption,
) error {
	ch, err := r.Run(ctx, userID, sessionID, msg, opts...)
	if err != nil {
		return err
	}
	return printResponse(ch)
}

func printResponse(eventChan <-chan *event.Event) error {
	var out strings.Builder
	for ev := range eventChan {
		if ev.Error != nil {
			return fmt.Errorf("model error: %s", ev.Error.Message)
		}
		if len(ev.Choices) == 0 {
			if ev.Done {
				break
			}
			continue
		}

		ch := ev.Choices[0]
		if ch.Delta.Content != "" {
			out.WriteString(ch.Delta.Content)
		}
		if ch.Message.Content != "" {
			out.WriteString(ch.Message.Content)
		}
		if ev.Done {
			break
		}
	}

	resp := strings.TrimSpace(out.String())
	if resp != "" {
		fmt.Printf("Assistant: %s\n", resp)
	}
	return nil
}
