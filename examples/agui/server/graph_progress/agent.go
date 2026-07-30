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

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

const progressEvent = "graph.node.progress"

func newGraphAgent() (*graphagent.GraphAgent, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("intake", progressNode("intake", 20, nil))
	sg.AddNode("outline", progressNode("outline", 40, nil))
	sg.AddNode("draft", progressNode("draft", 60, nil))
	sg.AddNode("review", progressNode("review", 80, nil))
	sg.AddNode("finish", progressNode("finish", 100, graph.State{
		graph.StateKeyLastResponse: "Graph progress demo finished.",
	}))
	sg.SetEntryPoint("intake")
	sg.AddEdge("intake", "outline")
	sg.AddEdge("outline", "draft")
	sg.AddEdge("draft", "review")
	sg.AddEdge("review", "finish")
	sg.SetFinishPoint("finish")
	compiled, err := sg.Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(appName, compiled)
}

func progressNode(node string, progress int, update graph.State) graph.NodeFunc {
	return func(ctx context.Context, _ graph.State) (any, error) {
		if err := emitProgress(ctx, node, progress); err != nil {
			return nil, err
		}
		if update == nil {
			return nil, nil
		}
		return update, nil
	}
}

func emitProgress(ctx context.Context, node string, progress int) error {
	run, ok := aguirunner.RunFromContext(ctx)
	if !ok {
		return nil
	}
	return run.Emit(ctx, aguievents.NewCustomEvent(
		progressEvent,
		aguievents.WithValue(map[string]any{
			"node":     node,
			"progress": progress,
		}),
	))
}
