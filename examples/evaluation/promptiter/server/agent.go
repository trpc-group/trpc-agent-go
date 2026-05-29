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

const defaultCandidateInstruction = "生成一篇中文体育战报"

func newCandidateAgent(m model.Model, instruction string) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(32768),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(generationConfig),
	), nil
}

func newBackwarderAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-backwarder",
		m,
	)
}

func newAggregatorAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-aggregator",
		m,
	)
}

func newOptimizerAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent(
		"promptiter-optimizer",
		m,
	)
}

func newPromptIterStageAgent(name string, m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(32768),
		Temperature: floatPtr(0.0),
	}
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newJudgeAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(32768),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		"commentary-judge",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
