//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mragent provides a GraphAgent runtime for MRAgent cue-tag-content
// active memory reconstruction.
package mragent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DefaultAgentName is the default name used by NewAgent.
	DefaultAgentName = "mragent-memory"

	nodeRewrite     = "rewrite"
	nodeReconstruct = "reconstruct"
	nodeTools       = "tools"
	nodePrune       = "prune"
	nodeAnswer      = "answer"
)

// NewAgent creates a GraphAgent that performs MRAgent active memory
// reconstruction with associative memory tools.
func NewAgent(llm model.Model, opts ...Option) (*graphagent.GraphAgent, error) {
	cfg := newOptions(opts...)
	g, err := NewGraph(llm, opts...)
	if err != nil {
		return nil, err
	}
	agentOpts := []graphagent.Option{
		graphagent.WithDescription(cfg.Description),
		graphagent.WithExecutorOptions(cfg.executorOptions()...),
	}
	agentOpts = append(agentOpts, cfg.GraphAgentOptions...)
	return graphagent.New(cfg.Name, g, agentOpts...)
}

// NewGraph creates the StateGraph used by NewAgent.
func NewGraph(llm model.Model, opts ...Option) (*graph.Graph, error) {
	if llm == nil {
		return nil, errors.New("mragent: model is required")
	}
	cfg := newOptions(opts...)
	tools := ToolMap(context.Background(), cfg.toolSetOptions()...)
	if len(tools) == 0 {
		return nil, errors.New("mragent: at least one memory tool is required")
	}

	sg := graph.NewStateGraph(stateSchema())
	sg.
		AddNode(nodeInit, initStateNode(cfg)).
		AddLLMNode(nodeRewrite, llm, cfg.RewriteInstruction, nil, graph.WithGenerationConfig(cfg.GenerationConfig)).
		AddNode(nodeAbsorbRewrite, absorbRewriteNode()).
		AddNode(nodePrepareRecon, prepareReconstructNode()).
		AddLLMNode(nodeReconstruct, llm, cfg.ReconstructionInstruction, tools, graph.WithGenerationConfig(cfg.GenerationConfig)).
		AddNode(nodeDecideRecon, decideReconstructNode()).
		AddToolsNode(nodeTools, tools, graph.WithEnableParallelTools(cfg.EnableParallelTools)).
		AddNode(nodeAbsorbTools, absorbToolsNode()).
		AddNode(nodePreparePrune, preparePruneNode()).
		AddLLMNode(nodePrune, llm, cfg.PruneInstruction, nil, graph.WithGenerationConfig(cfg.GenerationConfig)).
		AddNode(nodeAbsorbPrune, absorbPruneNode()).
		AddNode(nodePrepareAnswer, prepareAnswerNode()).
		AddLLMNode(nodeAnswer, llm, cfg.AnswerInstruction, nil, graph.WithGenerationConfig(cfg.GenerationConfig))

	sg.AddEdge(nodeInit, nodeRewrite)
	sg.AddEdge(nodeRewrite, nodeAbsorbRewrite)
	sg.AddEdge(nodeAbsorbRewrite, nodePrepareRecon)
	sg.AddEdge(nodePrepareRecon, nodeReconstruct)
	sg.AddEdge(nodeReconstruct, nodeDecideRecon)
	sg.AddConditionalEdges(
		nodeDecideRecon,
		func(ctx context.Context, state graph.State) (string, error) {
			return routeFromDecision(state, routePrune), nil
		},
		map[string]string{
			routeTools: nodeTools,
			routePrune: nodePreparePrune,
		},
	)
	sg.AddEdge(nodeTools, nodeAbsorbTools)
	sg.AddConditionalEdges(
		nodeAbsorbTools,
		func(ctx context.Context, state graph.State) (string, error) {
			return routeFromDecision(state, routeReconstruct), nil
		},
		map[string]string{
			routeReconstruct: nodePrepareRecon,
			routePrune:       nodePreparePrune,
		},
	)
	sg.AddEdge(nodePreparePrune, nodePrune)
	sg.AddEdge(nodePrune, nodeAbsorbPrune)
	sg.AddEdge(nodeAbsorbPrune, nodePrepareAnswer)
	sg.AddEdge(nodePrepareAnswer, nodeAnswer)
	sg.SetEntryPoint(nodeInit)
	sg.SetFinishPoint(nodeAnswer)

	g, err := sg.Compile()
	if err != nil {
		return nil, fmt.Errorf("mragent: compile graph: %w", err)
	}
	return g, nil
}

// ToolMap returns the unprefixed tools used by the MRAgent graph.
func ToolMap(ctx context.Context, opts ...ToolSetOption) map[string]tool.Tool {
	ts := NewToolSet(opts...)
	tools := ts.Tools(ctx)
	out := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		if t == nil || t.Declaration() == nil || t.Declaration().Name == "" {
			continue
		}
		out[t.Declaration().Name] = t
	}
	return out
}
