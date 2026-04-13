//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultModelName = "deepseek-chat"
	defaultLocation  = "Shenzhen"
	toolName         = "get_weather"
)

const llmInstruction = `You are a careful weather assistant.

Rules:
1. You must call the get_weather tool exactly once before answering.
2. Use the user's requested location as the tool argument.
3. Do not answer before you receive the tool result.`

func buildAgent(
	modelName string,
	baseURL string,
	apiKey string,
	service *flakyWeatherService,
	retryPolicy *tool.RetryPolicy,
) *llmagent.LLMAgent {
	weatherTool := function.NewFunctionTool(
		service.getWeather,
		function.WithName(toolName),
		function.WithDescription("Fetch the weather for a location."),
	)
	opts := []llmagent.Option{
		llmagent.WithModel(
			openai.New(
				modelName,
				openai.WithBaseURL(baseURL),
				openai.WithAPIKey(apiKey),
			),
		),
		llmagent.WithDescription("Demonstrates single tool-call retry on an LLMAgent."),
		llmagent.WithInstruction(llmInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(256),
			Temperature: floatPtr(0),
		}),
		llmagent.WithTools([]tool.Tool{weatherTool}),
	}
	if retryPolicy != nil {
		opts = append(opts, llmagent.WithToolCallRetryPolicy(retryPolicy))
	}
	return llmagent.New("llmagent-tool-call-retry-demo", opts...)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
