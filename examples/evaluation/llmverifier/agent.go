//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newCandidateAgent(modelName string, maxTokens int, temperature float64, opts ...openai.Option) agent.Agent {
	return llmagent.New(
		"candidate-agent",
		llmagent.WithModel(openai.New(modelName, opts...)),
		llmagent.WithInstruction("You are a helpful assistant."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(maxTokens),
			Temperature: floatPtr(temperature),
			Stream:      true,
		}),
	)
}

func newJudgeAgent(modelName string, maxTokens int, opts ...openai.Option) agent.Agent {
	logprobs := true
	topLogprobs := 20
	return llmagent.New(
		"judge-agent",
		llmagent.WithModel(openai.New(modelName, opts...)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(maxTokens),
			Temperature: floatPtr(0),
			Logprobs:    &logprobs,
			TopLogprobs: &topLogprobs,
			Stream:      false,
		}),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
