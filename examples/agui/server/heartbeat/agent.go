//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const agentName = "agui-heartbeat-agent"

func newAgent(
	modelName string,
	generationConfig model.GenerationConfig,
	quietPeriod time.Duration,
) *llmagent.LLMAgent {
	waitTool := newWaitTool(quietPeriod)
	return llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a concise assistant. "+
			"Start every user request by calling wait_before_answer exactly once. "+
			"After the tool result confirms that the quiet period has completed, answer the user."),
		llmagent.WithTools([]tool.Tool{waitTool}),
	)
}
