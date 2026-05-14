//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const calculatorDescription = "Run a basic arithmetic calculation."

type calculatorInput struct {
	A         float64 `json:"a" jsonschema:"description=Left operand,required"`
	B         float64 `json:"b" jsonschema:"description=Right operand,required"`
	Operation string  `json:"operation" jsonschema:"description=Operation: add, subtract, multiply, or divide.,required"`
}

type calculatorOutput struct {
	Expression string  `json:"expression" jsonschema:"description=Expression that was calculated"`
	Result     float64 `json:"result" jsonschema:"description=Calculation result"`
}

func calculatorTools() []tool.Tool {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription(calculatorDescription),
	)
	return []tool.Tool{calculatorTool}
}

func calculate(ctx context.Context, input calculatorInput) (calculatorOutput, error) {
	operation := strings.ToLower(strings.TrimSpace(input.Operation))
	result, expression, err := calculateResult(input.A, input.B, operation)
	if err != nil {
		return calculatorOutput{}, err
	}
	if inv, ok := agent.InvocationFromContext(ctx); ok {
		inv.SetState(calculatorCalledStateKey, true)
	}
	return calculatorOutput{
		Expression: expression,
		Result:     result,
	}, nil
}

func calculateResult(a, b float64, operation string) (float64, string, error) {
	switch operation {
	case "add", "+", "plus":
		return a + b, fmt.Sprintf("%g + %g", a, b), nil
	case "subtract", "-", "minus":
		return a - b, fmt.Sprintf("%g - %g", a, b), nil
	case "multiply", "*", "times":
		return a * b, fmt.Sprintf("%g * %g", a, b), nil
	case "divide", "/", "over":
		if b == 0 {
			return 0, "", fmt.Errorf("division by zero")
		}
		return a / b, fmt.Sprintf("%g / %g", a, b), nil
	default:
		return 0, "", fmt.Errorf("unsupported operation: %s", operation)
	}
}
