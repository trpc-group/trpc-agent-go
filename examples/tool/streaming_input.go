//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// streamingInputExample demonstrates streaming usage.
func streamingInputExample(ctx context.Context, llm *openai.Model) error {
	temperature := 0.9
	maxTokens := 1000

	getWeatherTool := function.NewFunctionTool(getWeather, function.WithName("get_weather"),
		function.WithDescription("Get weather at the given location"))
	getPopulationTool := function.NewFunctionTool(getPopulation, function.WithName("get_population"),
		function.WithDescription("Get population at the given city"))

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(
				"You are a helpful weather guide. If you don't have real-time " +
					"weather data, you should call the user-provided tool.",
			),
			model.NewUserMessage("What is the weather and population in London City? "),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      true,
		},
		Tools: map[string]tool.Tool{
			"get_weather":    getWeatherTool,
			"get_population": getPopulationTool,
		},
	}

	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Print("Streaming response: \n")
	var fullContent string
	// var toolCalls []model.ToolCall
	for response := range responseChan {
		if response.Error != nil {
			return fmt.Errorf("API error: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}

			if choice.FinishReason != nil {
				fmt.Printf("\nFinish reason: %s\n", *choice.FinishReason)
			}

			if err := handleToolCalls(choice, request, getWeatherTool, getPopulationTool); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleToolCalls processes tool calls in the response.
func handleToolCalls(choice model.Choice, request *model.Request, getWeatherTool, getPopulationTool tool.CallableTool) error {
	if len(choice.Message.ToolCalls) > 0 {
		// First, add the assistant message with tool calls.
		request.Messages = append(request.Messages, choice.Message)
	}

	for _, tc := range choice.Message.ToolCalls {
		switch tc.Function.Name {
		case "get_weather":
			if err := handleWeatherToolCall(tc, request, getWeatherTool); err != nil {
				return err
			}
		case "get_population":
			if err := handlePopulationToolCall(tc, request, getPopulationTool); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleWeatherToolCall handles weather tool calls.
func handleWeatherToolCall(tc model.ToolCall, request *model.Request, getWeatherTool tool.CallableTool) error {
	location := tc.Function.Arguments
	weatherData, err := getWeatherTool.Call(context.Background(), []byte(location))
	if err != nil {
		return fmt.Errorf("failed to call tool: %w", err)
	}
	bts, err := json.Marshal(weatherData)
	if err != nil {
		return fmt.Errorf("failed to marshal weather data: %w", err)
	}
	// Print the weather data
	fmt.Printf("CallTool at local: Weather in %s: %s\n", location, bts)
	request.Messages = append(request.Messages, model.Message{
		Role:    model.RoleTool,
		Content: string(bts),
		ToolID:  tc.ID,
	})
	return nil
}

// handlePopulationToolCall handles population tool calls.
func handlePopulationToolCall(tc model.ToolCall, request *model.Request, getPopulationTool tool.CallableTool) error {
	city := tc.Function.Arguments
	populationData, err := getPopulationTool.Call(context.Background(), []byte(city))
	if err != nil {
		return fmt.Errorf("failed to call tool: %w", err)
	}
	bts, err := json.Marshal(populationData)
	if err != nil {
		return fmt.Errorf("failed to marshal population data: %w", err)
	}
	// Print the population data
	fmt.Printf("CallTool at local: Population in %s: %s\n", city, bts)
	request.Messages = append(request.Messages, model.Message{
		Role:    model.RoleTool,
		Content: string(bts),
		ToolID:  tc.ID,
	})
	return nil
}
