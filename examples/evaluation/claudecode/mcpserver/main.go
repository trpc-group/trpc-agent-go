//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main implements a tiny STDIO MCP server used by the Claude Code evaluation example.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const calculatorToolName = "calculator"

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

func main() {
	server := mcp.NewStdioServer("eval-example-calculator", "1.0.0")
	server.RegisterTool(
		mcp.NewTool(
			calculatorToolName,
			mcp.WithDescription("Perform arithmetic operations including add, subtract, multiply, and divide."),
			mcp.WithString("operation", mcp.Required(), mcp.Description("Operation: add, subtract, multiply, or divide.")),
			mcp.WithNumber("a", mcp.Required(), mcp.Description("First operand.")),
			mcp.WithNumber("b", mcp.Required(), mcp.Description("Second operand.")),
		),
		handleCalculator,
	)
	if err := server.Start(); err != nil {
		log.Fatalf("mcp server failed: %v", err)
	}
}

func handleCalculator(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := parseCalculatorArgs(req)
	if err != nil {
		return nil, err
	}
	result, err := compute(args)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return mcp.NewTextResult(string(data)), nil
}

func parseCalculatorArgs(req *mcp.CallToolRequest) (calculatorArgs, error) {
	if req == nil || req.Params.Arguments == nil {
		return calculatorArgs{}, fmt.Errorf("missing arguments")
	}
	data, err := json.Marshal(req.Params.Arguments)
	if err != nil {
		return calculatorArgs{}, fmt.Errorf("marshal arguments: %w", err)
	}
	var args calculatorArgs
	if err := json.Unmarshal(data, &args); err != nil {
		return calculatorArgs{}, fmt.Errorf("unmarshal arguments: %w", err)
	}
	args.Operation = strings.TrimSpace(args.Operation)
	if args.Operation == "" {
		return calculatorArgs{}, fmt.Errorf("operation is empty")
	}
	return args, nil
}

func compute(args calculatorArgs) (calculatorResult, error) {
	op := strings.ToLower(args.Operation)
	var v float64
	switch op {
	case "add", "+":
		v = args.A + args.B
	case "subtract", "-":
		v = args.A - args.B
	case "multiply", "*":
		v = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		v = args.A / args.B
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    v,
	}, nil
}
