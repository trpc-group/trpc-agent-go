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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// localAgentConfig controls how the local code-search agent is wired up.
// The agent connects to our own MCP server (see comparison/../mcp/server.go)
// that exposes the AST-backed code_search tool over MCP.
type localAgentConfig struct {
	ModelName    string
	MCPServerURL string
	MCPToolName  string
}

type localCodeSearchAgentRunner struct {
	runner  runner.Runner
	toolSet *mcp.ToolSet
}

// newLocalCodeSearchAgentRunner builds an LLM agent whose only tool is the
// remote code_search exposed by our local MCP server. The MCP server owns the
// knowledge base (vector store + embedder + repo source + code_search) and is
// responsible for its own ingestion lifecycle.
func newLocalCodeSearchAgentRunner(cfg localAgentConfig) (*localCodeSearchAgentRunner, error) {
	toolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: cfg.MCPServerURL,
			Timeout:   30 * time.Second,
		},
		mcp.WithName("local-code-search-mcp"),
		mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter(cfg.MCPToolName)),
	)
	if err := toolSet.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("initialize local code-search MCP toolset: %w", err)
	}

	ag := llmagent.New(
		"local-code-search-agent",
		llmagent.WithModel(newAgentModel(cfg.ModelName)),
		llmagent.WithInstruction(localCodeSearchAgentInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
	)

	return &localCodeSearchAgentRunner{
		runner: runner.NewRunner(
			"code-context-engine-local-agent",
			ag,
			runner.WithSessionService(sessioninmemory.NewSessionService()),
		),
		toolSet: toolSet,
	}, nil
}

func (r *localCodeSearchAgentRunner) RunCase(ctx context.Context, c comparisonCase) (*agentRunResult, error) {
	if r == nil || r.runner == nil {
		return nil, fmt.Errorf("local code search agent runner is nil")
	}
	return runAgentInvocation(ctx, r.runner, localUserID, "local-"+c.Name, c.Prompt)
}

func (r *localCodeSearchAgentRunner) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	if r.runner != nil {
		if err := r.runner.Close(); err != nil {
			firstErr = err
		}
	}
	if r.toolSet != nil {
		if err := r.toolSet.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

const localCodeSearchAgentInstruction = `You are a repository code assistant.`
