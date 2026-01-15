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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// createMathAgent creates a specialized math agent.
func (c *transferChat) createMathAgent(modelInstance model.Model) agent.Agent {
	// Math calculation tool.
	calculateTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculate"),
		function.WithDescription("Perform mathematical calculations"),
	)

	genConfig := model.GenerationConfig{
		MaxCompletionTokens: intPtr(2000),
		Temperature:         floatPtr(0.3), // Lower temperature for more precise calculations.
		Stream:              true,
	}

	return llmagent.New(
		"math-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized mathematical computation agent"),
		llmagent.WithInstruction("You are a math expert. Solve mathematical problems step by step with clear explanations."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculateTool}),
	)
}

// calculate performs mathematical operations.
func (c *transferChat) calculate(_ context.Context, args calcArgs) (calcResult, error) {
	var result float64
	switch args.Operation {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return calcResult{
				Operation: args.Operation,
				A:         args.A,
				B:         args.B,
				Result:    0,
				Error:     "Division by zero",
			}, nil
		}
		result = args.A / args.B
	case "power":
		result = 1
		for i := 0; i < int(args.B); i++ {
			result *= args.A
		}
	default:
		return calcResult{
			Operation: args.Operation,
			A:         args.A,
			B:         args.B,
			Result:    0,
			Error:     "Unknown operation",
		}, nil
	}

	return calcResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// Data structures for math tool.
type calcArgs struct {
	Operation string  `json:"operation" jsonschema:"description=The operation to perform,enum=add,enum=subtract,enum=multiply,enum=divide,enum=power,required"`
	A         float64 `json:"a" jsonschema:"description=First number operand,required"`
	B         float64 `json:"b" jsonschema:"description=Second number operand,required"`
}

type calcResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
	Error     string  `json:"error,omitempty"`
}
