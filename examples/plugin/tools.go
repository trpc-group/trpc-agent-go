//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	toolNameCalculator = "calculator"
	toolDescCalculator = "Perform basic math operations."

	opAdd      = "add"
	opSubtract = "subtract"
	opMultiply = "multiply"
	opDivide   = "divide"
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

func newCalculatorTool() tool.Tool {
	return function.NewFunctionTool(
		calculate,
		function.WithName(toolNameCalculator),
		function.WithDescription(toolDescCalculator),
		function.WithInputSchema(calculatorInputSchema()),
	)
}

func calculatorInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "Calculator tool input.",
		Properties: map[string]*tool.Schema{
			"operation": {
				Type:        "string",
				Description: "One of add, subtract, multiply, divide.",
				Enum:        []any{opAdd, opSubtract, opMultiply, opDivide},
			},
			"a": {Type: "number", Description: "First number."},
			"b": {Type: "number", Description: "Second number."},
		},
		Required: []string{"operation", "a", "b"},
	}
}

func calculate(
	_ context.Context,
	args calculatorArgs,
) (calculatorResult, error) {
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
	default:
		out = 0
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    out,
	}, nil
}
