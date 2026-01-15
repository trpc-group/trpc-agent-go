//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// createWeatherAgent creates a specialized weather agent.
func (c *transferChat) createWeatherAgent(modelInstance model.Model) agent.Agent {
	// Weather tool.
	weatherTool := function.NewFunctionTool(
		c.getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get current weather information for a location"),
	)

	genConfig := model.GenerationConfig{
		MaxCompletionTokens: intPtr(1500),
		Temperature:         floatPtr(0.5),
		Stream:              true,
	}

	return llmagent.New(
		"weather-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized weather information agent"),
		llmagent.WithInstruction("You are a weather expert. Provide detailed weather information and recommendations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{weatherTool}),
	)
}

// getWeather returns weather information for a location.
func (c *transferChat) getWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	// Simulate weather data based on location.
	weather := map[string]weatherResult{
		"tokyo": {
			Location:       "Tokyo, Japan",
			Temperature:    22.5,
			Condition:      "Partly Cloudy",
			Humidity:       65,
			Recommendation: "Perfect weather for outdoor activities",
		},
		"london": {
			Location:       "London, UK",
			Temperature:    15.2,
			Condition:      "Rainy",
			Humidity:       85,
			Recommendation: "Bring an umbrella and dress warmly",
		},
		"new york": {
			Location:       "New York, USA",
			Temperature:    18.7,
			Condition:      "Sunny",
			Humidity:       45,
			Recommendation: "Great day for outdoor activities",
		},
	}

	location := strings.ToLower(args.Location)
	if result, exists := weather[location]; exists {
		return result, nil
	}

	// Default response for unknown locations.
	return weatherResult{
		Location:       args.Location,
		Temperature:    20.0,
		Condition:      "Clear",
		Humidity:       50,
		Recommendation: "Weather data not available, but looks pleasant",
	}, nil
}

// Data structures for weather tool.
type weatherArgs struct {
	Location string `json:"location" jsonschema:"description=The location to get weather for,required"`
}

type weatherResult struct {
	Location       string  `json:"location"`
	Temperature    float64 `json:"temperature"`
	Condition      string  `json:"condition"`
	Humidity       int     `json:"humidity"`
	Recommendation string  `json:"recommendation"`
}
