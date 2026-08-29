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
)

const (
	appName          = "trpcagent-travel-agent"
	agentInstruction = `You are a concise travel assistant.
Use this scenario when answering: Shanghai is sunny at 26C, festival events will be held downtown with likely traffic disruptions, and museum tickets are available with 25 seats left.
Answer the user's travel question with weather, alert, ticket availability, and practical advice.`
)

func newTravelAgent(m model.Model, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0),
		Stream:      stream,
	}
	return llmagent.New(
		appName,
		llmagent.WithModel(m),
		llmagent.WithDescription("Travel agent used by the tRPC-Agent evaluation example."),
		llmagent.WithInstruction(agentInstruction),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
