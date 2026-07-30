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

const defaultCandidateInstruction = "你是一名资深体育评论员。请生成一篇结构清晰、数据详实的中文战报，必须包含【战报】【数据面板】【战术分析】三个板块，总字数控制在 350–850 字之间（绝不少于 350 字）。"

// fakeBaselineInstruction is the plain baseline prompt used in -fake mode.
// The real-model default above already contains the structured sections the
// fake candidate recognises as "boosted", which would make the baseline score
// equal to the optimized score and leave the optimizer no room to demonstrate
// a gain. A deliberately plain instruction restores the intended demo:
// baseline (plain) -> optimizer proposes structured sections -> score improves.
const fakeBaselineInstruction = "你是一名体育评论员。请为给定的比赛信息撰写一段中文比赛评论。"

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
