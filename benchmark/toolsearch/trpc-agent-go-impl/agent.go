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
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/toolsearch/trpc-agent-go-impl/mathtools"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

func NewInstrumentedRunner(cfg BenchmarkConfig, collector *Collector) (runner.Runner, error) {
	chatModel := openai.New(cfg.ModelName)

	modelCallbacks := model.NewCallbacks()
	// Protect against concurrent callbacks updating shared collector.
	var mu sync.Mutex
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		usage, ok := toolsearch.ToolSearchUsageFromContext(ctx)
		if ok && usage != nil {
			sessionID, turnIndex, ok2 := sessionTurnFromContext(ctx)
			if ok2 {
				mu.Lock()
				collector.AddToolSearchUsage(sessionID, turnIndex, usage)
				mu.Unlock()
			}
			ctx = toolsearch.SetToolSearchUsage(ctx, nil)
		}
		return &model.BeforeModelResult{Context: ctx}, nil
	})

	instruction := "You MUST use the provided tools to answer the user's request. " +
		"Choose the most appropriate tool, call it, then answer using the tool result. " +
		"Do not answer directly without calling a tool."

	mathToolSet, err := mathtools.NewToolSet()
	if err != nil {
		return nil, fmt.Errorf("create mathtools toolset: %w", err)
	}

	ag := llmagent.New(
		"toolsearch-benchmark-agent",
		llmagent.WithModel(chatModel),
		llmagent.WithToolSets([]tool.ToolSet{mathToolSet}),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Benchmark agent for tool search evaluation"),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	plugins, err := buildPlugins(cfg, chatModel)
	if err != nil {
		return nil, err
	}

	sess := inmemory.NewSessionService()
	base := runner.NewRunner(cfg.AppName, ag,
		runner.WithSessionService(sess),
		runner.WithPlugins(plugins...),
	)
	return NewCountingRunner(base, collector), nil
}

func buildPlugins(cfg BenchmarkConfig, chatModel model.Model) ([]plugin.Plugin, error) {
	switch cfg.Mode {
	case ModeNone:
		return nil, nil
	case ModeLLMSearch:
		tp, err := toolsearch.New(chatModel, toolsearch.WithMaxTools(cfg.MaxTools))
		if err != nil {
			return nil, fmt.Errorf("create toolsearch plugin (llm): %w", err)
		}
		return []plugin.Plugin{tp}, nil
	case ModeKnowledgeSearch:
		toolKnowledge, err := toolsearch.NewToolKnowledge(
			openaiembedder.New(openaiembedder.WithModel(cfg.EmbedModel)),
			toolsearch.WithVectorStore(vectorinmemory.New()),
		)
		if err != nil {
			return nil, fmt.Errorf("create tool knowledge: %w", err)
		}
		tp, err := toolsearch.New(
			chatModel,
			toolsearch.WithMaxTools(cfg.MaxTools),
			toolsearch.WithToolKnowledge(toolKnowledge),
		)
		if err != nil {
			return nil, fmt.Errorf("create toolsearch plugin (knowledge): %w", err)
		}
		return []plugin.Plugin{tp}, nil
	default:
		return nil, fmt.Errorf("unknown mode: %s", cfg.Mode)
	}
}
