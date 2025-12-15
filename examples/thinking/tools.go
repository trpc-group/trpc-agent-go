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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// buildTools creates the tools for this demo.
func buildTools() []tool.Tool {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription(
			"Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)
	return []tool.Tool{calculatorTool, timeTool}
}

// calculate performs basic mathematical operations.
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
		} else {
			result = 0 // Handle division by zero.
		}
	default:
		result = 0
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// getCurrentTime returns current time information.
func getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST.
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST.
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST.
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}

	return timeResult{
		Timezone: timezone,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}, nil
}

// calculatorArgs represents arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" jsonschema:"description=The operation to perform,enum=add,enum=subtract,enum=multiply,enum=divide"`
	A         float64 `json:"a" jsonschema:"description=First number"`
	B         float64 `json:"b" jsonschema:"description=Second number"`
}

// calculatorResult represents the result of a calculation.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// timeArgs represents arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=Timezone or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}
