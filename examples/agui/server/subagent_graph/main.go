//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main exposes the LLMAgent+GraphAgent sub-agent demo as an AG-UI server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"

	openaigo "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	nodeA     = "A"
	nodeTools = "tools"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use for both coordinator and graph agent")
	isStream  = flag.Bool("stream", false, "Whether to stream responses")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/agui", "HTTP path")
)

func main() {
	flag.Parse()

	subAgent, err := buildGraphAgent(*modelName)
	if err != nil {
		log.Fatalf("failed to build graph sub-agent: %v", err)
	}

	mainModel := openai.New(*modelName)
	coord := llmagent.New(
		"llm-hub",
		llmagent.WithModel(mainModel),
		llmagent.WithDescription("Coordinator that handles chit-chat; forwards math to the graph sub-agent"),
		llmagent.WithInstruction(`You are a coordinator.
	- Handle general chat directly.
	- If the user asks for calculations or arithmetic reasoning, call transfer_to_agent with target math-graph.
	- Keep replies brief; when delegating, tell the user you're using the math graph.`),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      *isStream,
			Temperature: floatPtr(0.6),
			MaxTokens:   intPtr(2000),
		}),
		llmagent.WithSubAgents([]agent.Agent{subAgent}),
	)

	sessionService := inmemory.NewSessionService()

	r := runner.NewRunner("debug-agent", coord, runner.WithSessionService(sessionService))
	defer r.Close()

	server, err := agui.New(r, agui.WithPath(*path), agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath("/history"),
		agui.WithAppName("debug-agent"),
		agui.WithSessionService(inmemory.NewSessionService()),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}

	log.Infof("AG-UI: serving %q with sub-agent %q at http://%s%s", "debug-agent", subAgent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

// buildGraphAgent constructs the graph sub-agent (start=A, end=A with a calculator tools branch).
func buildGraphAgent(modelName string) (agent.Agent, error) {
	g, err := buildGraph(modelName)
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		"math-graph",
		g,
		graphagent.WithDescription("GraphAgent: node A is both entry and finish; calls calculator tools when needed"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithMessageTimelineFilterMode(processor.TimelineFilterCurrentInvocation),
		graphagent.WithMessageBranchFilterMode(processor.BranchFilterModeExact),
	)
}

// buildGraph wires node A with a conditional edge to tools and loops back after tool execution.
func buildGraph(modelName string) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	llm := openai.New(modelName, openai.WithChatRequestCallback(func(ctx context.Context, chatRequest *openaigo.ChatCompletionNewParams) {
		data, _ := json.Marshal(chatRequest)
		log.Infof("model request: %s", string(data))
	}))

	calcTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("Compute a basic arithmetic operation: add, subtract, multiply, divide."),
	)
	tools := map[string]tool.Tool{"calculator": calcTool}

	instruction := `You are node A inside a graph.
- If the user wants arithmetic, emit exactly one calculator tool call with fields: operation (add|subtract|multiply|divide), a, b.
- When a tool result arrives, summarize it briefly and avoid extra tool calls unless needed.
- If no tool is needed, answer directly and finish.`

	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		nodeA,
		llm,
		instruction,
		tools,
		graph.WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)
	sg.AddToolsNode(nodeTools, tools)
	sg.AddNode("user_modify", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyUserInput: "解决上述问题"}, nil
	})
	sg.AddToolsConditionalEdges(nodeA, nodeTools, graph.End)
	sg.AddEdge(nodeTools, "user_modify")
	sg.AddEdge("user_modify", nodeA)
	sg.SetEntryPoint(nodeA).SetFinishPoint(nodeA)

	return sg.Compile()
}

// calculator performs a simple arithmetic operation.
func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	op := strings.ToLower(strings.TrimSpace(args.Operation))
	var res float64

	switch op {
	case "add", "+":
		res = args.A + args.B
	case "subtract", "-":
		res = args.A - args.B
	case "multiply", "*", "x":
		res = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		res = args.A / args.B
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}

	return calculatorResult{
		Operation: op,
		A:         args.A,
		B:         args.B,
		Result:    res,
	}, nil
}

// calculatorArgs represents input for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

// calculatorResult represents the calculator output.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
