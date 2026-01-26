//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewCalculatorTool creates a calculator tool.
func NewCalculatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		calculateExpression,
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations. Supports basic operations (+ - * /), scientific functions (sin/cos/tan/sqrt/log/ln/abs/pow) and constants (pi/e). Examples: '2+3*4', 'sqrt(16)', 'sin(30*pi/180)', 'log10(100)'"),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Mathematical expression to calculate",
			Required:    []string{"expression"},
			Properties: map[string]*tool.Schema{
				"expression": {
					Type:        "string",
					Description: "Mathematical expression to calculate. Supports basic operations (+ - * /), scientific functions (sin/cos/tan/sqrt/log/ln/abs/pow) and constants (pi/e). Examples: '2+3*4', 'sqrt(16)', 'sin(30*pi/180)', 'log10(100)'",
				},
			},
		}),
	)
}

type calculatorRequest struct {
	Expression string `json:"expression"`
}

type calculatorResponse struct {
	Expression string  `json:"expression"`
	Result     float64 `json:"result"`
	Message    string  `json:"message"`
}

func calculateExpression(_ context.Context, req calculatorRequest) (calculatorResponse, error) {
	if strings.TrimSpace(req.Expression) == "" {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    "Error: Expression is empty",
		}, fmt.Errorf("expression is empty")
	}

	result, err := evaluateSimpleExpression(req.Expression)
	if err != nil {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    fmt.Sprintf("Calculation error: %v", err),
		}, fmt.Errorf("calculation error: %w", err)
	}

	return calculatorResponse{
		Expression: req.Expression,
		Result:     result,
		Message:    fmt.Sprintf("Calculation result: %g", result),
	}, nil
}

func evaluateSimpleExpression(expr string) (float64, error) {
	expr = strings.ReplaceAll(expr, "pi", fmt.Sprintf("%g", math.Pi))
	expr = strings.ReplaceAll(expr, "e", fmt.Sprintf("%g", math.E))
	expr = strings.ReplaceAll(expr, " ", "")

	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}

	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) == 2 {
			left, _ := evaluateSimpleExpression(parts[0])
			right, _ := evaluateSimpleExpression(parts[1])
			return left + right, nil
		}
	}

	if strings.Contains(expr, "-") && !strings.HasPrefix(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) == 2 {
			left, _ := evaluateSimpleExpression(parts[0])
			right, _ := evaluateSimpleExpression(parts[1])
			return left - right, nil
		}
	}

	if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) == 2 {
			left, _ := evaluateSimpleExpression(parts[0])
			right, _ := evaluateSimpleExpression(parts[1])
			return left * right, nil
		}
	}

	if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) == 2 {
			left, _ := evaluateSimpleExpression(parts[0])
			right, _ := evaluateSimpleExpression(parts[1])
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		}
	}

	if strings.HasPrefix(expr, "sqrt(") && strings.HasSuffix(expr, ")") {
		inner := expr[5 : len(expr)-1]
		val, _ := evaluateSimpleExpression(inner)
		if val < 0 {
			return 0, fmt.Errorf("cannot calculate square root of negative number")
		}
		return math.Sqrt(val), nil
	}

	if strings.HasPrefix(expr, "abs(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, _ := evaluateSimpleExpression(inner)
		return math.Abs(val), nil
	}

	if strings.HasPrefix(expr, "sin(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, _ := evaluateSimpleExpression(inner)
		return math.Sin(val), nil
	}

	if strings.HasPrefix(expr, "cos(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, _ := evaluateSimpleExpression(inner)
		return math.Cos(val), nil
	}

	return strconv.ParseFloat(expr, 64)
}
