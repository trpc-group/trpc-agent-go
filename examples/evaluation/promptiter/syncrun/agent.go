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
	candidateAgentName = "candidate"
)

const defaultCandidateInstruction = "Write one Chinese sentence that summarizes the JSON input. Output only the text."

func newCandidateAgent(m model.Model, instruction string) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      true,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Candidate agent for the PromptIter sports commentary example."),
		llmagent.WithGenerationConfig(generationConfig),
	), nil
}

func newBackwarderAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-backwarder",
		"Backwarder agent for the PromptIter sports commentary example.",
		m,
	)
}

func newAggregatorAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-aggregator",
		"Aggregator agent for the PromptIter sports commentary example.",
		m,
	)
}

func newOptimizerAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-optimizer",
		"Optimizer agent for the PromptIter sports commentary example.",
		m,
	)
}

func newPromptIterStageAgent(name string, description string, m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
	}
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithDescription(description),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newJudgeAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		"commentary-judge",
		llmagent.WithModel(m),
		llmagent.WithInstruction("Follow the provided evaluation instructions exactly. Treat the user input as structured JSON with current live game state and recent context. Return only the requested judge output."),
		llmagent.WithDescription("Judge agent for the PromptIter sports commentary rubric evaluation example."),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
