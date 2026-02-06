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

const (
	// rougeAgentName is the agent name used by the ROUGE evaluation example.
	rougeAgentName = "rouge-agent"
	// rougeAgentInstruction is used to keep the output short and stable for ROUGE matching.
	rougeAgentInstruction = "Answer in exactly one short sentence of plain text. Do not use markdown, lists, or code formatting. Output only the answer."
)

// newRougeAgent creates an LLM-based agent for the ROUGE evaluation example.
func newRougeAgent(modelName string, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(64),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		rougeAgentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(rougeAgentInstruction),
		llmagent.WithGenerationConfig(genCfg),
	)
}

// intPtr returns a pointer to v.
func intPtr(v int) *int { return &v }

// floatPtr returns a pointer to v.
func floatPtr(v float64) *float64 { return &v }
