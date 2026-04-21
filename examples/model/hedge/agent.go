//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func newHedgeAgent(config appConfig) (*llmagent.LLMAgent, error) {
	hedgeModel, err := newHedgeModel(config)
	if err != nil {
		return nil, err
	}
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1200),
		Temperature: floatPtr(0.7),
		Stream:      config.streaming,
	}
	return llmagent.New(
		agentName,
		llmagent.WithModel(hedgeModel),
		llmagent.WithDescription("A chat assistant backed by a hedged primary and backup model."),
		llmagent.WithInstruction("You are a concise and reliable assistant. Answer clearly and directly."),
		llmagent.WithGenerationConfig(genConfig),
	), nil
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
