//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the main package for the graph activity AG-UI server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	nodePrepare            = "prepare"
	nodeRecipeCalcLLM      = "recipe_calc_llm"
	nodeExecuteTools       = "execute_tools"
	nodeConfirm            = "confirm"
	nodeDraftMessageLLM    = "draft_message_llm"
	nodePolishMessageAgent = "polish_message_agent"
	nodeFinish             = "finish"
)

var (
	modelName = flag.String("model", "deepseek-chat", "OpenAI-compatible model name.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()

	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.2),
		Stream:      *isStream,
	}

	g, err := buildGraph(modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}

	checkpointSaver := inmemory.NewSaver()
	subAgent := llmagent.New(
		nodePolishMessageAgent,
		llmagent.WithDescription("Polishes the draft recipe message into a clear, friendly message."),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful cooking assistant. Rewrite the input as a clear, friendly message."),
	)
	ga, err := graphagent.New(
		"agui-graph-activity",
		g,
		graphagent.WithDescription("AG-UI server that emits activity events for graph node starts."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{subAgent}),
		graphagent.WithCheckpointSaver(checkpointSaver),
	)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}

	sessionService := sessioninmemory.NewSessionService()
	r := runner.NewRunner(ga.Info().Name, ga, runner.WithSessionService(sessionService))
	defer r.Close()

	server, err := agui.New(
		r,
		agui.WithPath(*path),
		agui.WithGraphNodeLifecycleActivityEnabled(true),
		agui.WithGraphNodeInterruptActivityEnabled(true),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithStateResolver(resolveRuntimeState),
		),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithSessionService(sessionService),
		agui.WithAppName(ga.Info().Name),
	)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}

	log.Infof("AG-UI: serving agent %q on http://%s%s", ga.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func buildGraph(modelInstance model.Model, generationConfig model.GenerationConfig) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)

	tools := map[string]tool.Tool{
		"calculator": function.NewFunctionTool(
			calculator,
			function.WithName("calculator"),
			function.WithDescription(`Perform basic arithmetic on two numbers.
Supported operations: add, subtract, multiply, divide.
Use this tool to compute totals and scaled values in everyday tasks.
Return a JSON object with a numeric result.`),
		),
	}

	sg.AddNode(nodePrepare, prepareNode)
	sg.AddLLMNode(
		nodeRecipeCalcLLM,
		modelInstance,
		`You are helping the user scale a cookie recipe.
Read the user's message and use the calculator tool to compute, in order:
1) scale = desired_servings / base_servings
2) flour_g = base_flour_g * scale
3) butter_g = base_butter_g * scale
4) sugar_g = base_sugar_g * scale
5) subtotal_g = flour_g + butter_g
6) total_g = subtotal_g + sugar_g
Rules:
- Call the calculator tool for each step above, in order.
- When calling the calculator tool, use operation values: add, subtract, multiply, divide.
- Do not write the final recipe message in this node.`,
		tools,
		graph.WithGenerationConfig(generationConfig),
	)
	sg.AddToolsNode(nodeExecuteTools, tools)
	sg.AddNode(nodeConfirm, confirmNode)
	sg.AddLLMNode(
		nodeDraftMessageLLM,
		modelInstance,
		`Write a final recipe message based on the conversation and calculator tool results.
Include:
- A scaled ingredient list.
- A short shopping list.
- Simple step-by-step instructions.`,
		nil,
		graph.WithGenerationConfig(generationConfig),
	)
	sg.AddAgentNode(nodePolishMessageAgent, graph.WithSubgraphInputFromLastResponse())
	sg.AddNode(nodeFinish, finishNode)

	sg.SetEntryPoint(nodePrepare)
	sg.AddEdge(nodePrepare, nodeRecipeCalcLLM)
	sg.AddToolsConditionalEdges(nodeRecipeCalcLLM, nodeExecuteTools, nodeConfirm)
	sg.AddEdge(nodeExecuteTools, nodeConfirm)
	sg.AddEdge(nodeConfirm, nodeDraftMessageLLM)
	sg.AddEdge(nodeDraftMessageLLM, nodePolishMessageAgent)
	sg.AddEdge(nodePolishMessageAgent, nodeFinish)
	sg.SetFinishPoint(nodeFinish)

	return sg.Compile()
}

func prepareNode(ctx context.Context, state graph.State) (any, error) {
	metadata := map[string]any{
		"example": "agui.server.graph",
	}
	return graph.State{graph.StateKeyMetadata: metadata}, nil
}

func confirmNode(ctx context.Context, state graph.State) (any, error) {
	message := "Confirm continuing after the recipe amounts are calculated."
	v, err := graph.Interrupt(ctx, state, nodeConfirm, message)
	if err != nil {
		return nil, err
	}
	confirmed, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("invalid confirmation value: %T", v)
	}
	if confirmed {
		return nil, nil
	}
	return &graph.Command{
		Update: graph.State{
			graph.StateKeyMetadata: map[string]any{
				"finish": "canceled",
			},
		},
		GoTo: graph.End,
	}, nil
}

func finishNode(ctx context.Context, state graph.State) (any, error) {
	metadata := map[string]any{
		"finish": "ok",
	}
	return graph.State{graph.StateKeyMetadata: metadata}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"Operation: add, subtract, multiply, divide."`
	A         float64 `json:"a" description:"First number."`
	B         float64 `json:"b" description:"Second number."`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	switch args.Operation {
	case "add":
		return calculatorResult{Result: args.A + args.B}, nil
	case "subtract":
		return calculatorResult{Result: args.A - args.B}, nil
	case "multiply":
		return calculatorResult{Result: args.A * args.B}, nil
	case "divide":
		if args.B == 0 {
			return calculatorResult{}, errors.New("division by zero")
		}
		return calculatorResult{Result: args.A / args.B}, nil
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}
}

func resolveRuntimeState(_ context.Context, input *aguiadapter.RunAgentInput) (map[string]any, error) {
	if input == nil {
		return nil, nil
	}
	state, _ := input.State.(map[string]any)
	if state == nil {
		return nil, nil
	}

	runtimeState := make(map[string]any)
	if lineageID, ok := state[graph.CfgKeyLineageID].(string); ok {
		runtimeState[graph.CfgKeyLineageID] = lineageID
	}
	if checkpointID, ok := state[graph.CfgKeyCheckpointID].(string); ok {
		runtimeState[graph.CfgKeyCheckpointID] = checkpointID
	}
	if resumeMap, ok := state[graph.CfgKeyResumeMap].(map[string]any); ok && len(resumeMap) > 0 {
		copied := make(map[string]any)
		maps.Copy(copied, resumeMap)
		runtimeState[graph.StateKeyCommand] = &graph.Command{ResumeMap: copied}
	}
	return runtimeState, nil
}

func intPtr(i int) *int { return &i }

func floatPtr(f float64) *float64 { return &f }
