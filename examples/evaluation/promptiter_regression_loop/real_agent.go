//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const realCandidateAgentName = "travel-support"

func newRealCandidateAgent(m model.Model, instruction string, cfg LLMConfig) (agent.Agent, error) {
	return llmagent.New(
		realCandidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithTools(newWeatherTools()),
		llmagent.WithGenerationConfig(realGenerationConfig(cfg, false)),
	), nil
}

func newRealJudgeAgent(m model.Model, cfg LLMConfig) agent.Agent {
	return newRealStageAgent("promptiter-regression-judge", m, cfg)
}

func newRealBackwarderAgent(m model.Model, cfg LLMConfig) agent.Agent {
	return newRealStageAgent("promptiter-regression-backwarder", m, cfg)
}

func newRealAggregatorAgent(m model.Model, cfg LLMConfig) agent.Agent {
	return newRealStageAgent("promptiter-regression-aggregator", m, cfg)
}

func newRealOptimizerAgent(m model.Model, cfg LLMConfig) agent.Agent {
	return newRealStageAgent("promptiter-regression-optimizer", m, cfg)
}

func newRealStageAgent(name string, m model.Model, cfg LLMConfig) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(realGenerationConfig(cfg, true)),
	)
}

func realGenerationConfig(cfg LLMConfig, stage bool) model.GenerationConfig {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		if stage {
			maxTokens = 8192
		} else {
			maxTokens = 2048
		}
	}
	temperature := cfg.Temperature
	return model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
		Stream:      false,
	}
}

func newWeatherTools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			lookupWeather,
			function.WithName("lookup_weather"),
			function.WithDescription("Look up deterministic weather records. Use the exact city and date requested by the user."),
		),
	}
}

type weatherArgs struct {
	City string `json:"city" jsonschema:"description=City name to look up,required"`
	Date string `json:"date" jsonschema:"description=Requested date,required"`
}

type weatherResult struct {
	City         string `json:"city"`
	Condition    string `json:"condition"`
	TemperatureC int    `json:"temperature_c"`
}

func lookupWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	switch args.City {
	case "Paris":
		return weatherResult{City: "Paris", Condition: "rainy", TemperatureC: 12}, nil
	case "Berlin":
		return weatherResult{City: "Berlin", Condition: "cloudy", TemperatureC: 8}, nil
	default:
		return weatherResult{City: args.City, Condition: "unknown", TemperatureC: 0}, nil
	}
}
