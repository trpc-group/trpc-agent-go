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

// newCandidateAgent builds the candidate agent whose instruction surface is the
// optimization target. The baseline instruction is read from the prompt file;
// later candidate instructions come from the PromptIter optimizer.
func newCandidateAgent(candidateName string, m model.Model, instruction string) (agent.Agent, error) {
	return llmagent.New(
		candidateName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(0.0),
			Stream:      false,
		}),
	), nil
}

// newStageAgent builds a PromptIter worker agent (backwarder / aggregator /
// optimizer) bound to the same deterministic fake model.
func newStageAgent(name string, m model.Model) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(4096),
			Temperature: floatPtr(0.0),
		}),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
