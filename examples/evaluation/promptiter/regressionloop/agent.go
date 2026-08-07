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
	regressionAppName           = "regression-app"
	candidateAgentName          = "regression-candidate"
	judgeAgentName              = "regression-judge"
	backwarderAgentName         = "regression-backwarder"
	aggregatorAgentName         = "regression-aggregator"
	optimizerAgentName          = "regression-optimizer"
	defaultCandidateInstruction = "Answer the request using only supported information."
)

func newCandidateAgent(instance model.Model, instruction string) agent.Agent {
	if instruction == "" {
		instruction = defaultCandidateInstruction
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(instance),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(deterministicGenerationConfig()),
	)
}

func newJudgeAgent(instance model.Model) agent.Agent {
	return newStageAgent(judgeAgentName, instance)
}

func newBackwarderAgent(instance model.Model) agent.Agent {
	return newStageAgent(backwarderAgentName, instance)
}

func newAggregatorAgent(instance model.Model) agent.Agent {
	return newStageAgent(aggregatorAgentName, instance)
}

func newOptimizerAgent(instance model.Model) agent.Agent {
	return newStageAgent(optimizerAgentName, instance)
}

func newStageAgent(name string, instance model.Model) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(instance),
		llmagent.WithGenerationConfig(deterministicGenerationConfig()),
	)
}

func deterministicGenerationConfig() model.GenerationConfig {
	maxTokens := 4096
	temperature := 0.0
	return model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
		Stream:      false,
	}
}
