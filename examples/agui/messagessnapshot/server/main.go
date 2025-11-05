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
	"flag"
	"fmt"
	"math"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName            = flag.String("model", "deepseek-chat", "Model to use")
	isStream             = flag.Bool("stream", true, "Whether to stream the response")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address")
	path                 = flag.String("path", "/agui", "HTTP path")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path")
)

const appName = "demo-app"

func main() {
	flag.Parse()
	// New Agent.
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.7),
		Stream:      *isStream,
	}
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	agent := llmagent.New(
		"agui-agent",
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant."),
	)
	// New Session Service.
	sessionService := inmemory.NewSessionService()
	// New Runner.
	runner := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	// New AG-UI server.
	server, err := agui.New(
		runner,
		agui.WithPath(*path),
		agui.WithMessagesSnapshotPath(*messagesSnapshotPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(userIDResolver)),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	// Start server.
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", *address, *messagesSnapshotPath)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}

}

func userIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
		return user, nil
	}
	return "anonymous", nil
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
