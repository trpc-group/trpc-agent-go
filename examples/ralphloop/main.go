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
	"context"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/ralphloop"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName = "ralph-demo"

	agentName = "demo-agent"

	deepSeekModelName = "deepseek-chat"
	deepSeekAPIKeyEnv = "DEEPSEEK_API_KEY"

	promiseValue = "DONE"

	maxIterations = 5
	maxLLMCalls   = 10
)

const agentInstruction = `This is a demo of Ralph Loop.

Rules:
1) On your first assistant response, do NOT output <promise>DONE</promise>.
   Reply with: Iteration 1: still working.
2) On your second assistant response, reply with: Iteration 2: done.
   Then output: <promise>DONE</promise> on its own line.
`

const userPrompt = "Follow your instructions."

func main() {
	if os.Getenv(deepSeekAPIKeyEnv) == "" {
		fmt.Printf("Set %s to run this example.\n", deepSeekAPIKeyEnv)
		return
	}

	llm := openai.New(
		deepSeekModelName,
		openai.WithVariant(openai.VariantDeepSeek),
	)

	rl, err := ralphloop.New(ralphloop.Config{
		MaxIterations:     maxIterations,
		CompletionPromise: promiseValue,
	})
	if err != nil {
		panic(err)
	}

	maxTokens := 512
	temperature := 0.2
	a := llmagent.New(
		agentName,
		llmagent.WithModel(llm),
		llmagent.WithPlanner(rl),
		llmagent.WithInstruction(agentInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
		}),
		llmagent.WithMaxLLMCalls(maxLLMCalls),
	)

	r := runner.NewRunner(appName, a)
	defer r.Close()

	ctx := context.Background()
	msg := model.NewUserMessage(userPrompt)

	events, err := r.Run(ctx, "user", "session", msg)
	if err != nil {
		panic(err)
	}

	for e := range events {
		if e == nil || e.Response == nil {
			continue
		}
		if e.Error != nil {
			fmt.Printf("Error: %s\n", e.Error.Message)
			continue
		}
		if e.IsRunnerCompletion() {
			break
		}
		if len(e.Choices) == 0 {
			continue
		}
		fmt.Print(e.Choices[0].Message.Content)
	}
}
