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
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newAgent() agent.Agent {
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(1.0),
		Stream:      *isStream,
	}
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	return llmagent.New(
		"agui-agent",
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant."),
	)
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
