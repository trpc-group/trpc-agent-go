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

const candidateAgentName = "candidate"

func newCandidateAgent(m model.Model, instruction string) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newJudgeAgent(m model.Model) agent.Agent {
	return newStageAgent("eval-optimization-judge", m)
}

func newBackwarderAgent(m model.Model) agent.Agent {
	return newStageAgent("eval-optimization-backwarder", m)
}

func newAggregatorAgent(m model.Model) agent.Agent {
	return newStageAgent("eval-optimization-aggregator", m)
}

func newOptimizerAgent(m model.Model) agent.Agent {
	return newStageAgent("eval-optimization-optimizer", m)
}

func newStageAgent(name string, m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      false,
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
