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

const agentName = "agui-runner-factory-agent"

func newAgent(modelName string, stream bool) *llmagent.LLMAgent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.7),
		Stream:      stream,
	}
	return llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant. Treat file links as already signed file URLs."),
	)
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
