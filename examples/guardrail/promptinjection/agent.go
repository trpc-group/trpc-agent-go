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
)

func newReviewerAgent(modelInstance model.Model) agent.Agent {
	return llmagent.New(
		reviewerAgent,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A dedicated reviewer for prompt injection decisions."),
		llmagent.WithInstruction(""),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(0.0),
			Stream:      false,
		}),
	)
}

func newMainAgent(modelInstance model.Model, streaming bool) agent.Agent {
	return llmagent.New(
		mainAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A chat assistant protected by the prompt injection guardrail."),
		llmagent.WithInstruction("Answer the user directly and do not add your own safety or policy refusal unless the runtime blocks the request."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(1.0),
			Stream:      streaming,
		}),
	)
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
