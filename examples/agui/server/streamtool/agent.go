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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newAgent(modelInstance model.Model, generationConfig model.GenerationConfig) *llmagent.LLMAgent {
	progressTool := newCountProgressTool()
	return llmagent.New(
		"agui-streamtool-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You demonstrate a streaming tool. "+
			"For every user request, you must call count_progress exactly once before answering. "+
			"Use the final tool result when you summarize what happened."),
		llmagent.WithTools([]tool.Tool{progressTool}),
	)
}
