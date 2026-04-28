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

func newQAAgent(modelName string, stream bool) agent.Agent {
	return llmagent.New(
		"template-eval-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Answer the user question with the minimal factual answer. Use a single word when possible."),
		llmagent.WithDescription("Simple QA agent for llm_judge_template evaluation."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(64),
			Temperature: floatPtr(0.0),
			Stream:      stream,
		}),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
