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

func newJiebaAgent(modelName string, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(64),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		"jieba-rewrite-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("你是一位中文改写助手，请将用户输入的中文改写成一句意思相近的中文。"),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
