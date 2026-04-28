//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	demotool "trpc.group/trpc-go/trpc-agent-go/examples/agui/server/externaltool/llmagent/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const agentInstruction = `You are a verification assistant for a mixed internal/external tool demo.

Rules:
1. Before answering, call calculator, internal_lookup, external_note, and external_approval exactly once based on the user request, and emit all four tool calls in the same assistant message.
2. If only some tool results are available, wait and do not call any extra tools.
3. Once all four tool results are available, do not call tools again.
4. Reply with exactly one line:
CALC=<integer>; LOOKUP=<internal lookup result>; NOTE=<external note content>; APPROVAL=<external approval content>
5. Copy the external_note and external_approval tool result content verbatim after NOTE= and APPROVAL=.`

func newAgent(modelInstance model.Model, generationConfig model.GenerationConfig) *llmagent.LLMAgent {
	return llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithTools(demotool.NewTools()),
		llmagent.WithEnableParallelTools(true),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction(agentInstruction),
	)
}

func newGenerationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0),
		Stream:      *isStream,
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
