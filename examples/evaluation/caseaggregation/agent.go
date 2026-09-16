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
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newSupportAgent(modelName string, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(128),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		caseAggregationAgentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(""+
			"You are a support assistant.\n"+
			"Answer the user in one clear, concise, and helpful sentence."),
		llmagent.WithDescription("Support assistant for account access questions."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
