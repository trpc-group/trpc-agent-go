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

func newCandidateTravelAgent(modelName string, stream bool) agent.Agent {
	genCfg := newGenerationConfig(4096, 1.0, stream, "")
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"You are a travel-planning assistant for short business trips in China. " +
				"Ask only the most relevant missing details before finalizing. " +
				"Do not ask for details the user already provided. " +
				"When enough information is available, give a concise but practical itinerary " +
				"covering transport, hotel area, and travel reminders. " +
				"If the user has not fixed every operational detail, you may proceed with reasonable business-friendly assumptions and state them briefly.",
		),
		llmagent.WithDescription("Travel-planning agent used as the candidate runner in the usersimulation_expectedrunner example."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation_expectedrunner-candidate",
		opts...,
	)
}

func newReferenceTravelAgent(modelName string, reasoningEffort string) agent.Agent {
	genCfg := newGenerationConfig(4096, 1.0, false, reasoningEffort)
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"You are the reference travel-planning assistant for short business trips in China. " +
				"Produce stable, business-friendly reference answers. " +
				"Ask only the most relevant remaining details, and keep clarification brief. " +
				"When enough information is available, provide a practical itinerary covering transport, hotel area, and travel reminders. " +
				"For Shanghai to Beijing business travel, prefer business-friendly defaults when needed: Hongqiao or Shanghai city departure can default toward SHA, Guomao/CBD usually aligns with Capital Airport, and a Thursday evening return is a reasonable default if the return time is not fixed. " +
				"When the user asks for hotel suggestions, provide a short, concrete shortlist by budget.",
		),
		llmagent.WithDescription("Travel-planning agent used as the expected runner in the usersimulation_expectedrunner example."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation_expectedrunner-reference",
		opts...,
	)
}

func newSimulatorAgent(modelName string) agent.Agent {
	genCfg := newGenerationConfig(1024, 1.0, false, "")
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"Follow the system instruction exactly and reply with only the next user utterance.",
		),
		llmagent.WithDescription("LLM agent used as the backing runner for the default user simulator."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation_expectedrunner-simulator",
		opts...,
	)
}

func newJudgeAgent(modelName string, reasoningEffort string) agent.Agent {
	genCfg := newGenerationConfig(2048, 1.0, false, reasoningEffort)
	opts := []llmagent.Option{
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction(
			"Follow the provided evaluation instructions exactly and return only the requested judge output.",
		),
		llmagent.WithDescription("Judge agent used by the usersimulation_expectedrunner evaluation example."),
		llmagent.WithGenerationConfig(genCfg),
	}
	return llmagent.New(
		"usersimulation_expectedrunner-judge",
		opts...,
	)
}

func newGenerationConfig(maxTokens int, temperature float64, stream bool, reasoningEffort string) model.GenerationConfig {
	cfg := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      stream,
	}
	if reasoningEffort != "" {
		cfg.ReasoningEffort = stringPtr(reasoningEffort)
	}
	return cfg
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

func stringPtr(v string) *string {
	return &v
}
