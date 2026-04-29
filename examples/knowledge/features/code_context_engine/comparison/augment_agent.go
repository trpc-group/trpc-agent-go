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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

type augmentCodeAgentRunner struct {
	runner  runner.Runner
	toolSet *mcp.ToolSet
}

func newAugmentCodeAgentRunner(modelName string) (*augmentCodeAgentRunner, error) {
	headers := map[string]string{}
	if key := strings.TrimSpace(util.GetEnvOrDefault("AUGMENT_CONTEXT_ENGINE_API_KEY", "")); key != "" {
		headerValue := key
		if !strings.HasPrefix(strings.ToLower(key), "bearer ") {
			headerValue = "Bearer " + key
		}
		headers[augmentAuthHeader] = headerValue
	}

	toolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable_http",
			ServerURL: augmentServerURL,
			Timeout:   30 * time.Second,
			Headers:   headers,
		},
		mcp.WithName("augment-context-engine"),
		mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter(augmentToolName)),
	)
	if err := toolSet.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("initialize augment MCP toolset: %w", err)
	}

	ag := llmagent.New(
		"augment-code-search-agent",
		llmagent.WithModel(newAgentModel(modelName)),
		llmagent.WithInstruction(augmentCodeSearchAgentInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream: true,
		}),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
	)

	return &augmentCodeAgentRunner{
		runner: runner.NewRunner(
			"code-context-engine-augment-agent",
			ag,
			runner.WithSessionService(sessioninmemory.NewSessionService()),
		),
		toolSet: toolSet,
	}, nil
}

func (r *augmentCodeAgentRunner) RunCase(ctx context.Context, c comparisonCase) (*agentRunResult, error) {
	if r == nil || r.runner == nil {
		return nil, fmt.Errorf("augment agent runner is nil")
	}
	return runAgentInvocation(ctx, r.runner, augmentUserID, "augment-"+c.Name, c.Prompt)
}

func (r *augmentCodeAgentRunner) Close() error {
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

const augmentCodeSearchAgentInstruction = `You are a repository code assistant.

When calling augment_code_search, always use:
- repo_owner: trpc-group
- repo_name:  trpc-agent-go
- branch:     main
`
