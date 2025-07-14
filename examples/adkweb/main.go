// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the Apache 2.0 License.
// A copy of the Apache 2.0 License is included in this file.
//
// Package main provides a standalone CLI demo showcasing how to wire the
// trpc-agent-go orchestration layer with an LLM agent that exposes two simple tools:
// a calculator and a time query. It starts an HTTP server compatible with ADK Web UI
// for manual testing.
//
// This file demonstrates how to set up a simple LLM agent with custom tools
// (calculator and time query) and expose it via an HTTP server compatible with the
// ADK Web UI. It is intended for manual testing and as a reference for integrating
// tRPC agent orchestration with LLM-based tools.
//
// The example covers:
// - Model and tool setup
// - Agent configuration
// - HTTP server integration
//
// Usage:
//
//	go run main.go
//
// The server will listen on :8080 by default.
//
// Author: Tencent, 2025
//
// -----------------------------------------------------------------------------
package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/debug"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const defaultListenAddr = ":8080"

// main is the entry point of the application.
// It sets up an LLM agent with calculator and time tools,
// and starts an HTTP server for ADK Web UI compatibility.
func main() {
	// Parse command-line flags for server address.
	var addr string
	flag.StringVar(&addr, "addr", defaultListenAddr, "Listen address")
	flag.Parse()

	// --- Model and tools setup ---
	// Create the OpenAI model instance for LLM interactions.
	modelName := "deepseek-chat"
	modelInstance := openai.New(modelName, openai.Options{})

	// Create calculator tool for mathematical operations.
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription(
			// Perform basic mathematical calculations (add, subtract, multiply, divide).
			"Perform basic mathematical calculations "+
				"(add, subtract, multiply, divide)",
		),
	)
	// Create time tool for timezone queries.
	timeTool := function.NewFunctionTool(
		getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription(
			// Get the current time and date for a specific timezone.
			"Get the current time and date for a specific "+
				"timezone",
		),
	)

	// Configure generation parameters for the LLM.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	// Create the LLM agent with tools and configuration.
	agentName := "assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"A helpful AI assistant with calculator and time tools",
		),
		llmagent.WithInstruction(
			"Use tools when appropriate for calculations or time queries. "+
				"Be helpful and conversational.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools(
			[]tool.Tool{calculatorTool, timeTool},
		),
	)

	// Register the agent in the agent map.
	agents := map[string]agent.Agent{
		agentName: llmAgent,
	}

	// Create the debug server for HTTP interface.
	server := debug.New(agents)

	// Start the HTTP server and handle requests.
	log.Infof(
		// Log the server listening address and registered agents.
		"CLI server listening on %s (apps: %v)",
		addr,
		agents,
	)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Constants & helpers ----------------------------------------------------------
// -----------------------------------------------------------------------------

// Constants for supported calculator operations.
const (
	opAdd      = "add"
	opSubtract = "subtract"
	opMultiply = "multiply"
	opDivide   = "divide"
)

// calculatorArgs holds the input for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// calculatorResult holds the output for the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// timeArgs holds the input for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone " +
		"(UTC, EST, PST, CST) or leave empty for local"`
}

// timeResult holds the output for the time tool.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Calculator tool implementation.
// calculate performs the requested mathematical operation.
// It supports add, subtract, multiply, and divide operations.
func calculate(args calculatorArgs) calculatorResult {
	var result float64
	// Select operation based on input.
	switch strings.ToLower(args.Operation) {
	case opAdd:
		result = args.A + args.B
	case opSubtract:
		result = args.A - args.B
	case opMultiply:
		result = args.A * args.B
	case opDivide:
		if args.B != 0 {
			result = args.A / args.B
		}
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// Time tool implementation.
// getCurrentTime returns the current time for the specified timezone.
// If the timezone is invalid or empty, it defaults to local time.
func getCurrentTime(args timeArgs) timeResult {
	loc := time.Local
	zone := args.Timezone
	// Attempt to load the specified timezone.
	if zone != "" {
		var err error
		loc, err = time.LoadLocation(zone)
		if err != nil {
			loc = time.Local
		}
	}
	now := time.Now().In(loc)
	return timeResult{
		Timezone: loc.String(),
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Weekday().String(),
	}
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 {
	return &f
}

// This example demonstrates how to integrate tRPC agent orchestration
// with LLM-based tools, providing a simple HTTP server for manual
// testing. It is intended as a reference for developers looking to build
// custom LLM agents with tool support in Go.
//
// The calculator tool supports basic arithmetic operations, while the
// time tool provides current time information for a given timezone.
//
// The code is structured for clarity and ease of extension.
