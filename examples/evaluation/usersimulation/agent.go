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

func newTravelPlannerAgent(modelName string, stream bool) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.2),
		Stream:      stream,
	}
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"You are a travel-planning assistant for business trips. " +
				"Ask for missing constraints before finalizing. " +
				"When enough information is available, provide a concise itinerary " +
				"covering transport, hotel area, and a short checklist.",
		),
		llmagent.WithDescription("Travel-planning agent used by the usersimulation evaluation example."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation-travel-planner",
		opts...,
	)
}

func newSimulatorAgent(modelName string) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.2),
		Stream:      false,
	}
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"Follow the system instruction exactly and reply with only the next user utterance.",
		),
		llmagent.WithDescription("LLM agent used as the backing runner for the default user simulator."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation-simulator",
		opts...,
	)
}

func newJudgeAgent(modelName string) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"Follow the provided evaluation instructions exactly and return only the requested judge output.",
		),
		llmagent.WithDescription("Judge agent used by the usersimulation evaluation example."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation-judge",
		opts...,
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
