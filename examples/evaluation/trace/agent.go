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

func newTraceAgent(modelName string, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.2),
		Stream:      stream,
	}

	return llmagent.New(
		"trace-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Answer the user concisely and accurately."),
		llmagent.WithDescription("A simple LLM agent used to create a real runner for trace evaluation examples."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
