//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"errors"
	"fmt"

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const calculatorName = "calculator"

func newCalculatorTool() agenttool.Tool {
	return function.NewFunctionTool(
		calculator,
		function.WithName(calculatorName),
		function.WithDescription("Add, subtract, multiply, or divide two integers."),
	)
}

func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result int
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, errors.New("division by zero")
		}
		result = args.A / args.B
	default:
		return calculatorResult{}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string `json:"operation" description:"add, subtract, multiply, or divide"`
	A         int    `json:"a" description:"The first integer."`
	B         int    `json:"b" description:"The second integer."`
}

type calculatorResult struct {
	Result int `json:"result"`
}
