//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates per-run GenerationConfig overrides using
// graph call options.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName = "call-options-generation-config"

	parentAgentName = "parent"
	childAgentName  = "child_agent"

	nodeParentLLM = "parent_llm"
	nodeChildLLM  = "llm"

	userID    = "user"
	userInput = "hello"

	parentTempDefault = 0.8
	childTempDefault  = 0.7

	callTempGlobal = 0.2
	callTempChild  = 0.0

	callMaxTokensParent = 111
	callMaxTokensChild  = 222

	assistantOK = "ok"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	parentModel := &printModel{name: nodeParentLLM}
	childModel := &printModel{name: nodeChildLLM}

	childGraph, err := buildChildGraph(childModel)
	if err != nil {
		return err
	}
	childAgent, err := graphagent.New(
		childAgentName,
		childGraph,
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("new child graphagent: %w", err)
	}

	parentGraph, err := buildParentGraph(parentModel)
	if err != nil {
		return err
	}
	parentAgent, err := graphagent.New(
		parentAgentName,
		parentGraph,
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	if err != nil {
		return fmt.Errorf("new parent graphagent: %w", err)
	}

	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		parentAgent,
		runner.WithSessionService(sessSvc),
	)
	defer r.Close()

	fmt.Println("Run 1: defaults (compile-time WithGenerationConfig)")
	if err := runOnce(ctx, r, newSessionID()); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Run 2: with call options (per-run overrides)")
	runOpt := buildCallOptions()
	if err := runOnce(ctx, r, newSessionID(), runOpt); err != nil {
		return err
	}
	return nil
}

func buildChildGraph(m model.Model) (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		nodeChildLLM,
		m,
		"child",
		nil,
		graph.WithGenerationConfig(model.GenerationConfig{
			Temperature: model.Float64Ptr(childTempDefault),
		}),
	)
	sg.SetEntryPoint(nodeChildLLM)
	sg.SetFinishPoint(nodeChildLLM)
	return sg.Compile()
}

func buildParentGraph(m model.Model) (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		nodeParentLLM,
		m,
		"parent",
		nil,
		graph.WithGenerationConfig(model.GenerationConfig{
			Temperature: model.Float64Ptr(parentTempDefault),
		}),
	)
	sg.AddSubgraphNode(childAgentName)
	sg.AddEdge(nodeParentLLM, childAgentName)
	sg.SetEntryPoint(nodeParentLLM)
	sg.SetFinishPoint(childAgentName)
	return sg.Compile()
}

func buildCallOptions() agent.RunOption {
	return graph.WithCallOptions(
		graph.WithCallGenerationConfigPatch(model.GenerationConfigPatch{
			Temperature: model.Float64Ptr(callTempGlobal),
		}),
		graph.DesignateNode(
			nodeParentLLM,
			graph.WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callMaxTokensParent),
			}),
		),
		graph.DesignateNode(
			childAgentName,
			graph.WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callMaxTokensChild),
			}),
		),
		graph.DesignateNodeWithPath(
			graph.NodePath{childAgentName, nodeChildLLM},
			graph.WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				Temperature: model.Float64Ptr(callTempChild),
			}),
		),
	)
}

func runOnce(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	runOpts ...agent.RunOption,
) error {
	evCh, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(userInput),
		runOpts...,
	)
	if err != nil {
		return err
	}
	for range evCh {
	}
	return nil
}

func newSessionID() string {
	return fmt.Sprintf("sess-%d", time.Now().UnixNano())
}

type printModel struct {
	name string
}

func (m *printModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	_ = ctx
	cfg := req.GenerationConfig
	fmt.Printf(
		"[%s] max_tokens=%s temperature=%s stop=%v\n",
		m.name,
		fmtInt(cfg.MaxTokens),
		fmtFloat(cfg.Temperature),
		cfg.Stop,
	)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Index:   0,
				Message: model.NewAssistantMessage(assistantOK),
			},
		},
	}
	close(ch)
	return ch, nil
}

func (m *printModel) Info() model.Info { return model.Info{Name: m.name} }

func fmtInt(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
}

func fmtFloat(p *float64) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%v", *p)
}
