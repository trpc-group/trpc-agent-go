//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newAgent(modelName string, streaming bool) *llmagent.LLMAgent {
	maxTokens := 1200
	temperature := 0.2
	return llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("You are a helpful support assistant."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      streaming,
		}),
	)
}
