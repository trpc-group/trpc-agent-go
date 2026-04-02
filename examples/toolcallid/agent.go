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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	agentName    = "toolcallid-assistant"
	toolNameCalc = "calculator"
	opAdd        = "add"
	opSubtract   = "subtract"
	opMultiply   = "multiply"
	opDivide     = "divide"
)

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

func newAgent(modelName string, variant string, streaming bool) *llmagent.LLMAgent {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName(toolNameCalc),
		function.WithDescription("Perform basic math operations."),
	)
	modelInstance := openai.New(
		modelName,
		openai.WithVariant(openai.Variant(variant)),
	)
	return llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An assistant used to demonstrate canonical tool call IDs with a calculator tool."),
		llmagent.WithInstruction(
			"For every user request, you must call the tool named calculator exactly once before answering. "+
				"Use one of add, subtract, multiply, divide. After the tool returns, answer in one concise sentence.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      streaming,
			Temperature: floatPtr(0),
			MaxTokens:   intPtr(1200),
		}),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)
}

func calculate(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	callID, _ := tool.ToolCallIDFromContext(ctx)
	printCalculatorExecution(callID, args)
	var out float64
	switch strings.ToLower(args.Operation) {
	case opAdd:
		out = args.A + args.B
	case opSubtract:
		out = args.A - args.B
	case opMultiply:
		out = args.A * args.B
	case opDivide:
		if args.B != 0 {
			out = args.A / args.B
		}
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    out,
	}, nil
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
