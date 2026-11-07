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
	"trpc.group/trpc-go/trpc-agent-go/examples/guardrail/approval/internal/hostcontext"
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

func newMainAgent(modelInstance model.Model, streaming bool, toolSet tool.ToolSet, baseDir string) agent.Agent {
	return llmagent.New(
		mainAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A host-exec assistant protected by the approval guardrail."),
		llmagent.WithInstruction("Help the user with scoped host tasks. Respect approval denials and user restrictions. Report actual tool results; do not retry denied actions to bypass review."),
		llmagent.WithToolCallbacks(hostcontext.Callbacks(baseDir)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(1.0),
			Stream:      streaming,
		}),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
		llmagent.WithMaxLLMCalls(8),
	)
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
