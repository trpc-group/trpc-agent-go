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
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
	graphAgentName  = "assistant"
	nodeStart       = "start"
	nodePrepare     = "prepare"
	nodeRoute       = "route"
	nodeTools       = "tools"
	nodeBranchA     = "branch_a"
	nodeBranchB     = "branch_b"
	nodeBranchNever = "branch_never"
	nodeJoin        = "join"
	nodeDone        = "done"
)

func buildAgent() (*graphagent.GraphAgent, []string, error) {
	compiled, err := buildGraph()
	if err != nil {
		return nil, nil, err
	}
	ag, err := graphagent.New(graphAgentName, compiled, graphagent.WithMaxConcurrency(1))
	if err != nil {
		return nil, nil, err
	}
	return ag, []string{
		graphAgentName + "/" + nodeStart,
		graphAgentName + "/" + nodePrepare,
		graphAgentName + "/" + nodeRoute,
		graphAgentName + "/" + nodeTools,
		graphAgentName + "/" + nodeBranchA,
		graphAgentName + "/" + nodeBranchB,
		graphAgentName + "/" + nodeBranchNever,
		graphAgentName + "/" + nodeJoin,
		graphAgentName + "/" + nodeDone,
	}, nil
}

func buildGraph() (*graph.Graph, error) {
	schema := graph.NewStateSchema().
		AddField("route_count", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
			Default: func() any { return 0 },
		}).
		AddField("visited", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})
	builder := graph.NewStateGraph(schema)
	builder.AddNode(nodeStart, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeStart}}, nil
	})
	builder.AddNode(nodePrepare, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodePrepare}}, nil
	})
	builder.AddNode(nodeRoute, func(_ context.Context, state graph.State) (any, error) {
		count, _ := state["route_count"].(int)
		return graph.State{"route_count": count + 1, "visited": []string{nodeRoute}}, nil
	})
	builder.AddNode(nodeTools, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeTools}}, nil
	})
	builder.AddNode(nodeBranchA, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeBranchA}}, nil
	})
	builder.AddNode(nodeBranchB, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeBranchB}}, nil
	})
	builder.AddNode(nodeBranchNever, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeBranchNever}}, nil
	})
	builder.AddNode(nodeJoin, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeJoin}}, nil
	})
	builder.AddNode(nodeDone, func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{nodeDone}}, nil
	})
	builder.SetEntryPoint(nodeStart)
	builder.AddEdge(nodeStart, nodeRoute)
	builder.AddEdge(nodeStart, nodePrepare)
	builder.AddConditionalEdges(nodeRoute, func(_ context.Context, state graph.State) (string, error) {
		count, _ := state["route_count"].(int)
		if count == 1 {
			return nodeTools, nil
		}
		return nodeBranchA, nil
	}, map[string]string{
		nodeTools:   nodeTools,
		nodeBranchA: nodeBranchA,
	})
	builder.AddEdge(nodeTools, nodeRoute)
	builder.AddEdge(nodePrepare, nodeBranchB)
	builder.AddJoinEdge([]string{nodeBranchA, nodeBranchB}, nodeJoin)
	builder.AddConditionalEdges(nodeJoin, func(context.Context, graph.State) (string, error) {
		return nodeDone, nil
	}, map[string]string{
		nodeDone:        nodeDone,
		nodeBranchNever: nodeBranchNever,
	})
	builder.SetFinishPoint(nodeDone)
	return builder.Compile()
}
