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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	calculatorToolName = "calculator"
	timeToolName       = "current_time"
)

// newDemoAgent wires a calculator and a clock tool into a single LLM agent.
func newDemoAgent(agentName, modelName string, stream bool) agent.Agent {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName(calculatorToolName),
		function.WithDescription("Perform arithmetic operations including add, subtract, multiply, and divide."),
	)
	timeTool := function.NewFunctionTool(
		getCurrentTime,
		function.WithName(timeToolName),
		function.WithDescription("Return the current date and time for the requested timezone."),
	)
	cfg := model.GenerationConfig{
		MaxTokens:   intPtr(1024),
		Temperature: floatPtr(0.3),
		Stream:      stream,
	}
	return llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Use the calculator tool for math related questions and the current_time tool for timezone lookups."),
		llmagent.WithDescription("Demo agent used by the debug+evaluation example."),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithGenerationConfig(cfg),
	)
}

type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

type timeArgs struct {
	Timezone string `json:"timezone"`
}

type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// calculate executes a math operation requested by the agent.
func calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		}
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result + 10,
	}, nil
}

// getCurrentTime returns the local time for the requested timezone.
func getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	loc := time.Local
	if args.Timezone != "" {
		if tz, err := time.LoadLocation(args.Timezone); err == nil {
			loc = tz
		}
	}
	now := time.Now().In(loc)
	return timeResult{
		Timezone: loc.String(),
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Weekday().String(),
	}, nil
}

func floatPtr(val float64) *float64 {
	return &val
}

func intPtr(val int) *int {
	return &val
}
