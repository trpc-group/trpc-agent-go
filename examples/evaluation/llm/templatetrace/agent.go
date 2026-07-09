//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	traceAgentName        = "template-trace-agent"
	nodeWeatherLLM        = "weather_llm"
	nodeWeatherLookup     = "weather_lookup"
	weatherLLMInstruction = "Use get_weather when a question asks for current weather, then answer concisely using the weather lookup result."
)

func newTraceAgent(modelName string) (agent.Agent, error) {
	weatherTool := function.NewFunctionTool(
		getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get current weather for a city."),
	)
	tools := map[string]tool.Tool{
		"get_weather": weatherTool,
	}
	m := openai.New(modelName)
	maxTokens := 512
	temperature := 1.0
	generationConfig := model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
		Stream:      false,
	}
	compiled, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			nodeWeatherLLM,
			m,
			weatherLLMInstruction,
			tools,
			graph.WithGenerationConfig(generationConfig),
		).
		AddToolsNode(nodeWeatherLookup, tools).
		AddToolsConditionalEdges(nodeWeatherLLM, nodeWeatherLookup, graph.End).
		AddEdge(nodeWeatherLookup, nodeWeatherLLM).
		SetEntryPoint(nodeWeatherLLM).
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		traceAgentName,
		compiled,
		graphagent.WithDescription("Weather graph agent for llm_judge_template trace evaluation."),
		graphagent.WithInitialState(graph.State{}),
	)
}

type weatherArgs struct {
	City string `json:"city"`
}

type weatherResult struct {
	City         string `json:"city"`
	Condition    string `json:"condition"`
	TemperatureF int    `json:"temperatureF"`
}

func getWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	return weatherResult{
		City:         args.City,
		Condition:    "sunny",
		TemperatureF: 72,
	}, nil
}
