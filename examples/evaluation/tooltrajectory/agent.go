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

// newToolAgent builds an agent with get_weather/get_news/get_time/get_ticket tools.
func newToolAgent(modelName string, stream bool) agent.Agent {
	weatherTool := function.NewFunctionTool(
		getWeather,
		function.WithName("get_weather"),
		function.WithDescription("Get weather for a city."),
	)
	newsTool := function.NewFunctionTool(
		getNews,
		function.WithName("get_news"),
		function.WithDescription("Get safety/news alerts for a city."),
	)
	timeTool := function.NewFunctionTool(
		getTime,
		function.WithName("get_time"),
		function.WithDescription("Get current timestamp."),
	)
	ticketTool := function.NewFunctionTool(
		getTicket,
		function.WithName("get_ticket"),
		function.WithDescription("Check ticket availability for a city and time."),
	)

	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}

	return llmagent.New(
		"tooltrajectory-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{weatherTool, newsTool, timeTool, ticketTool}),
		llmagent.WithInstruction(`Always call tools to answer:
- Use get_weather to fetch city weather.
- Use get_news for city alerts.
- Use get_time to get the current timestamp.
- Then call get_ticket with city and the time from get_time.
Finally summarize weather, alerts, ticket status, and travel advice in Chinese.`),
		llmagent.WithDescription("Agent for tool trajectory evaluation."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

type weatherArgs struct {
	City string `json:"city"`
}

type weatherResult struct {
	City      string  `json:"city"`
	Condition string  `json:"condition"`
	TempC     float64 `json:"tempC"`
}

func getWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	return weatherResult{
		City:      args.City,
		Condition: "sunny",
		TempC:     26.0,
	}, nil
}

type newsArgs struct {
	City string `json:"city"`
}

type newsResult struct {
	City     string `json:"city"`
	Headline string `json:"headline"`
}

func getNews(_ context.Context, args newsArgs) (newsResult, error) {
	return newsResult{
		City:     args.City,
		Headline: "Festival events will be held in the city center; please be aware of traffic disruptions.",
	}, nil
}

type timeArgs struct {
}

type timeResult struct {
	Timestamp string `json:"timestamp"`
}

func getTime(_ context.Context, _ timeArgs) (timeResult, error) {
	return timeResult{
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil
}

type ticketArgs struct {
	City string `json:"city"`
	Time string `json:"time"`
}

type ticketResult struct {
	City      string `json:"city"`
	Time      string `json:"time"`
	SeatsLeft int    `json:"seatsLeft"`
}

func getTicket(_ context.Context, args ticketArgs) (ticketResult, error) {
	return ticketResult{
		City:      args.City,
		Time:      args.Time,
		SeatsLeft: 25,
	}, nil
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
