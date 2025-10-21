//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates exporting a StateGraph to Graphviz DOT and PNG.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
	nodePrepare  = "prepare"
	nodeAsk      = "ask"
	nodeTools    = "tools"
	nodeFallback = "fallback"
	nodeNoop     = "noop"

	graphTitle = "Visualization Demo"
)

func main() {
	// Build a small demo graph (function -> llm -> tools OR fallback -> end)
	schema := graph.NewStateSchema()
	sg := graph.NewStateGraph(schema)

	// Function node (prepare)
	sg.AddNode(nodePrepare, func(ctx context.Context, s graph.State) (any, error) {
		return s, nil
	})

	// LLM node (visual only; we mark type for nicer styling)
	sg.AddNode(nodeAsk, func(ctx context.Context, s graph.State) (any, error) {
		return s, nil
	}, graph.WithNodeType(graph.NodeTypeLLM))

	// Tools node (visual only)
	sg.AddNode(nodeTools, func(ctx context.Context, s graph.State) (any, error) {
		return s, nil
	}, graph.WithNodeType(graph.NodeTypeTool))

	// Fallback function node
	sg.AddNode(nodeFallback, func(ctx context.Context, s graph.State) (any, error) {
		return s, nil
	})

	// Wiring
	sg.AddEdge(nodePrepare, nodeAsk)
	sg.AddToolsConditionalEdges(nodeAsk, nodeTools, nodeFallback)
	sg.SetEntryPoint(nodePrepare).SetFinishPoint(nodeFallback)

	// Optional: declare dynamic destinations for visualization
	// (rendered dotted and do not affect runtime)
	// e.g., ask could jump directly to end in certain designs
	sg.AddNode(nodeNoop, func(ctx context.Context, s graph.State) (any, error) { return s, nil },
		graph.WithDestinations(map[string]string{graph.End: "finish early"}))

	g := sg.MustCompile()

	// Write DOT
	dotPath := fmt.Sprintf("visualization-%d.dot", time.Now().Unix())
	if err := os.WriteFile(dotPath, []byte(g.DOT(
		graph.WithRankDir(graph.RankDirLR),
		graph.WithIncludeDestinations(true),
		graph.WithGraphLabel(graphTitle),
	)), 0o644); err != nil {
		fmt.Printf("failed to write DOT: %v\n", err)
		return
	}
	fmt.Printf("DOT written to %s\n", dotPath)

	// Try to render PNG if Graphviz is available.
	pngPath := strings.TrimSuffix(dotPath, ".dot") + ".png"
	if err := g.RenderImage(context.Background(), graph.ImageFormatPNG, pngPath,
		graph.WithRankDir(graph.RankDirLR),
		graph.WithIncludeDestinations(true),
		graph.WithGraphLabel(graphTitle),
	); err != nil {
		fmt.Printf("PNG render skipped (install Graphviz 'dot' to enable): %v\n", err)
		return
	}
	fmt.Printf("PNG rendered to %s\n", pngPath)
}
