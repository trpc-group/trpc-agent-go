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
)

const candidateAgentName = "candidate"

// defaultCandidateInstruction is the intentionally weak baseline instruction that the
// optimization loop will try to improve. The sample data (see data/) is designed so this
// baseline fails a subset of cases, giving PromptIter something to fix.
const defaultCandidateInstruction = "You classify a customer support message. Reply with the category."

func newCandidateAgent(m model.Model, instruction string) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2048),
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

func newJudgeAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2048),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		"regression-loop-judge",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newBackwarderAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent("promptiter-backwarder", m)
}

func newAggregatorAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent("promptiter-aggregator", m)
}

func newOptimizerAgent(m model.Model) agent.Agent {
	return newPromptIterStageAgent("promptiter-optimizer", m)
}

func newPromptIterStageAgent(name string, m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
	}
	return llmagent.New(
		name,
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
