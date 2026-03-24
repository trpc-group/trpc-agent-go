//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	nodeStart   = "start"
	nodePrepare = "prepare"
	nodeRoute   = "route"
	nodeTools   = "tools"
	nodeBranchA = "branch_a"
	nodeBranchB = "branch_b"
	nodeJoin    = "join"
	nodeDone    = "done"
)

func buildAgent() (*graphagent.GraphAgent, error) {
	compiled, err := buildGraph()
	if err != nil {
		return nil, err
	}
	return graphagent.New("assistant", compiled)
}

func buildGraph() (*graph.Graph, error) {
	toolSet := map[string]tool.Tool{
		"search_docs": function.NewFunctionTool(
			func(context.Context, searchDocsInput) (searchDocsOutput, error) {
				return searchDocsOutput{
					Results: []string{
						"Routing note: use tools when the request lacks evidence.",
					},
				}, nil
			},
			function.WithName("search_docs"),
			function.WithDescription("Look up reference snippets before taking the next branch."),
		),
		"summarize": function.NewFunctionTool(
			func(context.Context, summarizeInput) (summarizeOutput, error) {
				return summarizeOutput{
					Summary: "Summarized intermediate findings.",
				}, nil
			},
			function.WithName("summarize"),
			function.WithDescription("Summarize intermediate findings before returning to routing."),
		),
	}
	sg := graph.NewStateGraph(graph.NewStateSchema())
	sg.AddNode(nodeStart, func(context.Context, graph.State) (any, error) { return nil, nil })
	sg.AddNode(nodePrepare, func(context.Context, graph.State) (any, error) { return nil, nil })
	sg.AddLLMNode(
		nodeRoute,
		openai.New("gpt-4o-mini"),
		"Route the request. Use tools when more evidence is needed, otherwise continue directly to the business branch.",
		toolSet,
	)
	sg.AddToolsNode(nodeTools, toolSet)
	sg.AddNode(nodeBranchA, func(context.Context, graph.State) (any, error) { return nil, nil })
	sg.AddNode(nodeBranchB, func(context.Context, graph.State) (any, error) { return nil, nil })
	sg.AddNode(
		nodeJoin,
		func(context.Context, graph.State) (any, error) { return nil, nil },
		graph.WithEndsMap(map[string]string{
			"finish": nodeDone,
			"retry":  nodeStart,
		}),
	)
	sg.AddNode(nodeDone, func(context.Context, graph.State) (any, error) { return nil, nil })
	sg.SetEntryPoint(nodeStart)
	sg.AddEdge(nodeStart, nodeRoute)
	sg.AddEdge(nodeStart, nodePrepare)
	sg.AddToolsConditionalEdges(nodeRoute, nodeTools, nodeBranchA)
	sg.AddEdge(nodeTools, nodeRoute)
	sg.AddEdge(nodePrepare, nodeBranchB)
	sg.AddJoinEdge([]string{nodeBranchA, nodeBranchB}, nodeJoin)
	sg.AddConditionalEdges(nodeJoin, func(context.Context, graph.State) (string, error) {
		return "finish", nil
	}, nil)
	sg.SetFinishPoint(nodeDone)
	return sg.Compile()
}

type searchDocsInput struct {
	Query string `json:"query"`
}

type searchDocsOutput struct {
	Results []string `json:"results"`
}

type summarizeInput struct {
	Notes []string `json:"notes"`
}

type summarizeOutput struct {
	Summary string `json:"summary"`
}
