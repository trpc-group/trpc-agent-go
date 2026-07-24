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
		MaxTokens:   intPtr(256),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		supportAgentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(""+
			"You are a support assistant.\n"+
			"Answer the user in one concise sentence.\n"+
			"Every answer must include these exact lowercase words: please, support, help.\n"+
			"Do not say 'cannot help' or \"can't help\"."),
		llmagent.WithDescription("Support agent demonstrating metric extension evaluation."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
