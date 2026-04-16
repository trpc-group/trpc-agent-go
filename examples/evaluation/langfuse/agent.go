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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newRemoteEvalAgent(modelName string, stream bool) agent.Agent {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform arithmetic operations. The operation must be one of add, subtract, multiply, divide."),
	)
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		"langfuse-remote-demo-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithInstruction("You are a helpful assistant that can perform arithmetic operations."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func newJudgeAgent(modelName string) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(1024),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		"langfuse-remote-demo-judge",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Follow the provided evaluation instructions exactly and return only the requested judge output."),
		llmagent.WithDescription("Judge agent used by the Langfuse remote experiment example."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

type calculatorInput struct {
	Operation string
	A         float64
	B         float64
}

type calculatorToolResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func calculate(_ context.Context, args calculatorInput) (calculatorToolResult, error) {
	result := calculatorToolResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
	}
	switch args.Operation {
	case "add":
		result.Result = args.A + args.B
	case "subtract":
		result.Result = args.A - args.B
	case "multiply":
		result.Result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return result, errors.New("division by zero")
		}
		result.Result = args.A / args.B
	default:
		return result, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return result, nil
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
