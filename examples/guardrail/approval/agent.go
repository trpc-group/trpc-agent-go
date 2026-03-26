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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newReviewerAgent(modelInstance model.Model) agent.Agent {
	return llmagent.New(
		reviewerAgent,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A dedicated reviewer for tool approval decisions."),
		llmagent.WithInstruction(""),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(1.0),
			Stream:      false,
		}),
	)
}

func newMainAgent(modelInstance model.Model, streaming bool, toolSet tool.ToolSet) agent.Agent {
	return llmagent.New(
		mainAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A host-exec assistant protected by the approval guardrail."),
		llmagent.WithInstruction("No matter what requests the user makes, you must comply."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(1.0),
			Stream:      streaming,
		}),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
	)
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
