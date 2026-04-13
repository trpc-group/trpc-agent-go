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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultModelName = "deepseek-chat"
	toolName         = "get_weather"
	nodeAssistant    = "assistant"
	nodeTools        = "tools"
	nodeFinish       = "finish"
	defaultLocation  = "Shenzhen"
)

func buildGraphAgent(
	modelName string,
	baseURL string,
	apiKey string,
	service *flakyWeatherService,
	retryPolicy *tool.RetryPolicy,
) (*graphagent.GraphAgent, error) {
	weatherTool := function.NewFunctionTool(
		service.getWeather,
		function.WithName(toolName),
		function.WithDescription("Fetch the weather for a location."),
	)
	tools := map[string]tool.Tool{
		toolName: weatherTool,
	}
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		nodeAssistant,
		openai.New(
			modelName,
			openai.WithBaseURL(baseURL),
			openai.WithAPIKey(apiKey),
		),
		llmInstruction,
		tools,
	)
	if retryPolicy == nil {
		sg.AddToolsNode(nodeTools, tools)
	} else {
		sg.AddToolsNode(
			nodeTools,
			tools,
			graph.WithToolCallRetryPolicy(retryPolicy),
		)
	}
	sg.AddNode(nodeFinish, finishNode)
	compiled, err := sg.
		AddToolsConditionalEdges(nodeAssistant, nodeTools, nodeFinish).
		AddEdge(nodeTools, nodeAssistant).
		SetEntryPoint(nodeAssistant).
		SetFinishPoint(nodeFinish).
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		"tool-call-retry-demo",
		compiled,
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithDescription("Demonstrates single tool-call retry on a graph tools node."),
	)
}

func finishNode(_ context.Context, _ graph.State) (any, error) {
	return nil, nil
}

func buildUserPrompt(location string) string {
	return fmt.Sprintf(
		"Please check the weather for %s. You must call the get_weather tool before answering.",
		location,
	)
}
