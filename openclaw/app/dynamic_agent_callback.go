//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

const (
	dynamicAgentLLMBudgetBlocker = "dynamic_agent stopped before " +
		"completing because its LLM-call budget was exhausted. Treat " +
		"this as a recoverable tool blocker: use already collected " +
		"evidence, narrow the request, or try direct tools; do not " +
		"repeat the same broad worker request."
	dynamicAgentTimeoutBlocker = "dynamic_agent stopped before completing " +
		"because its timeout was reached. Treat this as a recoverable " +
		"tool blocker: use already collected evidence, narrow the " +
		"request, or try direct tools; do not repeat the same broad " +
		"worker request."
)

func registerDynamicAgentBlockerCallback(callbacks *tool.Callbacks) {
	if callbacks == nil {
		return
	}
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		if args == nil ||
			args.ToolName != agenttool.DefaultDynamicToolName ||
			args.Error == nil {
			return nil, nil
		}
		if dynamicAgentStoppedByLLMBudget(args.Error) {
			return &tool.AfterToolResult{
				CustomResult: dynamicAgentLLMBudgetBlocker,
			}, nil
		}
		if dynamicAgentStoppedByTimeout(args.Error) {
			return &tool.AfterToolResult{
				CustomResult: dynamicAgentTimeoutBlocker,
			}, nil
		}
		return nil, nil
	})
}

func dynamicAgentStoppedByLLMBudget(err error) bool {
	if err == nil {
		return false
	}
	if stopErr, ok := agent.AsStopError(err); ok {
		return strings.Contains(
			strings.ToLower(stopErr.Message),
			"max llm calls",
		)
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"max llm calls",
	)
}

func dynamicAgentStoppedByTimeout(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"deadline exceeded",
	)
}
