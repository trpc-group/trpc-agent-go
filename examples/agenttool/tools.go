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
	"strings"
	"time"
)

// calculate performs basic mathematical calculations.
func (c *agentToolChat) calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
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
			return calculatorResult{
				Operation: args.Operation,
				A:         args.A,
				B:         args.B,
				Result:    0,
				Error:     "Division by zero",
			}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	default:
		return calculatorResult{
			Operation: args.Operation,
			A:         args.A,
			B:         args.B,
			Result:    0,
			Error:     "Unknown operation",
		}, fmt.Errorf("unknown operation")
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// getCurrentTime returns the current time for a specific timezone.
func (c *agentToolChat) getCurrentTime(ctx context.Context, args timeArgs) (timeResult, error) {
	loc := time.Local
	if args.Timezone != "" {
		switch strings.ToUpper(args.Timezone) {
		case "UTC":
			loc = time.UTC
		case "EST":
			loc = time.FixedZone("EST", -5*3600)
		case "PST":
			loc = time.FixedZone("PST", -8*3600)
		case "CST":
			loc = time.FixedZone("CST", -6*3600)
		}
	}

	now := time.Now().In(loc)
	return timeResult{
		Timezone: args.Timezone,
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Format("Monday"),
	}, nil
}

// calculatorArgs defines the input arguments for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" jsonschema:"description=The operation: add, subtract, multiply, divide,enum=add,enum=subtract,enum=multiply,enum=divide"`
	A         float64 `json:"a" jsonschema:"description=First number"`
	B         float64 `json:"b" jsonschema:"description=Second number"`
}

// calculatorResult defines the output result for the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
	Error     string  `json:"error,omitempty"`
}

// timeArgs defines the input arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult defines the output result for the time tool.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Helper functions for creating pointers.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
