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
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	graphcheckpoint "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func newGraphAgent(g *graph.Graph) (*graphagent.GraphAgent, error) {
	return graphagent.New(
		agentName,
		g,
		graphagent.WithDescription("AG-UI server demo for external tool execution."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(graphcheckpoint.NewSaver()),
	)
}

func newGenerationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.2),
		Stream:      *isStream,
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
