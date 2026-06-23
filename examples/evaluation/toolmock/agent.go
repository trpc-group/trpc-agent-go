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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newWeatherAgent(modelName string, stream bool) agent.Agent {
	weatherTool := function.NewFunctionTool(
		getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get weather for a city."),
	)
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		"toolmock-weather-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{weatherTool}),
		llmagent.WithInstruction(`Always call get_weather before answering.
Use city "Shenzhen" when the user asks about Shenzhen.
Answer in one short sentence after reading the tool result.`),
		llmagent.WithDescription("Weather agent demonstrating evaluation tool mocks."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

type weatherArgs struct {
	City string `json:"city"`
}

type weatherResult struct {
	City      string `json:"city"`
	Condition string `json:"condition"`
	TempC     int    `json:"tempC"`
	Source    string `json:"source"`
	UpdatedAt string `json:"updatedAt"`
}

func getWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	return weatherResult{
		City:      args.City,
		Condition: "rainy",
		TempC:     18,
		Source:    "real-tool",
		UpdatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
