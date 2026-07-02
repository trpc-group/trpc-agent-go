//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main exposes a tRPC-Agent API server example.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	appName   = flag.String("app", "calculator", "tRPC-Agent app name")
	basePath  = flag.String("base-path", "/trpc-agent/v1/apps", "tRPC-Agent API base path")
	modelName = flag.String("model", "deepseek-v4-flash", "Model to use")
	timeout   = flag.Duration("timeout", 2*time.Minute, "Per-request timeout")
)

func main() {
	flag.Parse()
	agent := newAgent()
	agentRunner := runner.NewRunner(*appName, agent)
	defer agentRunner.Close()
	server, err := trpcagent.New(
		trpcagent.WithAppName(*appName),
		trpcagent.WithAgent(agent),
		trpcagent.WithRunner(agentRunner),
		trpcagent.WithBasePath(*basePath),
		trpcagent.WithTimeout(*timeout),
	)
	if err != nil {
		log.Fatalf("failed to create tRPC-Agent API server: %v", err)
	}
	log.Infof("tRPC-Agent API: serving app %q on http://%s%s/%s", *appName, *address, *basePath, *appName)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func newAgent() *llmagent.LLMAgent {
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("Performs add, subtract, multiply, divide, and power operations."),
	)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}
	return llmagent.New(
		"calculator-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithInstruction("You are a concise assistant. Use the calculator tool when arithmetic is needed."),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)
}

func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation %q", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"Operation to apply: add, subtract, multiply, divide, or power."`
	A         float64 `json:"a" description:"First operand."`
	B         float64 `json:"b" description:"Second operand."`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
