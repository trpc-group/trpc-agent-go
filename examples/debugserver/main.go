//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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
//	go run main.go -model gpt-4 -addr :9090
//
// The server will listen on :8080 by default and use deepseek-chat model.
//
// Author: Tencent, 2025
//
// -----------------------------------------------------------------------------
package main

import (
	"flag"
	"net/http"

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
	// Parse command-line flags for server address and model.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	addr := flag.String("addr", defaultListenAddr, "Listen address")
	flag.Parse()

	// --- Model and tools setup ---
	// Create the OpenAI model instance for LLM interactions.
	modelInstance := openai.New(*modelName)

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
		// Log the server listening address, model, and registered agents.
		"CLI server listening on %s (model: %s, apps: %v)",
		*addr,
		*modelName,
		agents,
	)
	// Start the HTTP server and handle requests.
	// This is a test server, so we don't need to use a more secure server.
	//nolint:gosec
	if err := http.ListenAndServe(*addr, server.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
