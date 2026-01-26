//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using AG-UI server with an A2A agent backend.
// This example:
// 1. Creates a local A2A server with an LLM agent that has two tools (getCurrentTime, calculate)
// 2. Creates an A2A agent client that connects to the local A2A server
// 3. Exposes the A2A agent through AG-UI protocol for CopilotKit frontend
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName   = flag.String("model", "deepseek-chat", "Model name to use")
	a2aHost     = flag.String("a2a-host", "127.0.0.1:8888", "A2A server listen address")
	aguiAddress = flag.String("agui-address", "127.0.0.1:8080", "AG-UI server listen address")
	aguiPath    = flag.String("agui-path", "/agui", "AG-UI HTTP path")
	streaming   = flag.Bool("streaming", true, "Enable streaming mode")
)

func main() {
	flag.Parse()

	// Step 1: Start local A2A server with an LLM agent that has tools
	log.Infof("Starting A2A server on %s", *a2aHost)
	startA2AServer(*a2aHost)
	time.Sleep(500 * time.Millisecond) // Wait for server to start

	// Step 2: Create A2A agent client that connects to the local A2A server
	a2aURL := fmt.Sprintf("http://%s", *a2aHost)
	log.Infof("Creating A2A agent client for: %s", a2aURL)

	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(a2aURL),
	)
	if err != nil {
		log.Fatalf("Failed to create A2A agent: %v", err)
	}

	// Display connected agent info
	card := a2aAgent.GetAgentCard()
	log.Infof("Connected to A2A agent: %s", card.Name)
	log.Infof("  Description: %s", card.Description)
	log.Infof("  URL: %s", card.URL)
	if card.Capabilities.Streaming != nil {
		log.Infof("  Streaming: %v", *card.Capabilities.Streaming)
	}

	// Step 3: Create runner with the A2A agent
	agentRunner := runner.NewRunner(card.Name, a2aAgent)
	defer agentRunner.Close()

	// Step 4: Create AG-UI server
	aguiServer, err := agui.New(agentRunner, agui.WithPath(*aguiPath))
	if err != nil {
		log.Fatalf("Failed to create AG-UI server: %v", err)
	}

	log.Infof("AG-UI: serving A2A agent %q on http://%s%s", card.Name, *aguiAddress, *aguiPath)
	if err := http.ListenAndServe(*aguiAddress, aguiServer.Handler()); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

// startA2AServer creates and starts an A2A server with an LLM agent that has two tools.
func startA2AServer(host string) {
	// Create LLM agent with tools
	llmAgent := createLLMAgentWithTools()

	// Create A2A server
	server, err := a2a.New(
		a2a.WithAgent(llmAgent, *streaming),
		a2a.WithHost(host),
		a2a.WithDebugLogging(false),
	)
	if err != nil {
		log.Fatalf("Failed to create A2A server: %v", err)
	}

	// Start server in background
	go func() {
		log.Infof("A2A server listening on %s", host)
		if err := server.Start(host); err != nil {
			log.Errorf("A2A server error: %v", err)
		}
	}()
}

// createLLMAgentWithTools creates an LLM agent with two tools: getCurrentTime and calculate.
func createLLMAgentWithTools() *llmagent.LLMAgent {
	modelInstance := openai.New(*modelName)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}

	tools := []tool.Tool{
		function.NewFunctionTool(
			getCurrentTime,
			function.WithName("getCurrentTime"),
			function.WithDescription("Get current time for a specific timezone. Returns time, date, and weekday."),
		),
		function.NewFunctionTool(
			calculate,
			function.WithName("calculate"),
			function.WithDescription("Perform basic arithmetic calculations. Supports add, subtract, multiply, divide operations."),
		),
	}

	agent := llmagent.New(
		"a2a-demo-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A demo agent with time and calculator tools, exposed via A2A protocol"),
		llmagent.WithInstruction("You are a helpful assistant with access to time and calculator tools. Use these tools to help users with their queries."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	return agent
}

// ============ Tool: getCurrentTime ============

type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=Timezone (UTC, EST, PST, CST) or leave empty for local time"`
}

type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

func getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour)
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour)
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour)
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

// ============ Tool: calculate ============

type calcArgs struct {
	Operation string  `json:"operation" jsonschema:"description=Operation type: add, subtract, multiply, divide"`
	A         float64 `json:"a" jsonschema:"description=First number"`
	B         float64 `json:"b" jsonschema:"description=Second number"`
}

type calcResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func calculate(_ context.Context, args calcArgs) (calcResult, error) {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*", "x":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calcResult{}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	default:
		return calcResult{}, fmt.Errorf("unknown operation: %s (supported: add, subtract, multiply, divide)", args.Operation)
	}

	return calcResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// ============ Helper functions ============

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
