//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// Criterion 7 (#2002): PermissionPolicy must refuse high-risk tools before
// execution and leave an auditable event. These cases drive the real
// safety.Guard through executeToolWithCallbacks (same path as production).

func TestSafetyGuard_FlowDenySkipsExecutionAndAudits(t *testing.T) {
	t.Parallel()
	auditor := safety.NewMemoryAuditor()
	guard := safety.NewGuard(safety.WithAuditor(auditor))

	var calledTool bool
	p := NewFunctionCallResponseProcessor(false, nil)
	tl := &mockCallableTool{
		declaration: &tool.Declaration{Name: "workspace_exec"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			calledTool = true
			return map[string]any{"ok": true}, nil
		},
	}
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicy(guard)),
	}
	args, err := json.Marshal(map[string]any{"command": "rm -rf /"})
	require.NoError(t, err)

	_, res, _, _, _, err := p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-deny-rm",
			Function: model.FunctionDefinitionParam{
				Name:      "workspace_exec",
				Arguments: args,
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	require.False(t, calledTool, "denied command must not execute")

	body := mustJSON(res)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "denied", payload["status"])
	require.Equal(t, "workspace_exec", payload["tool"])
	require.NotEmpty(t, payload["reason"])

	events := auditor.Events()
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	require.Equal(t, safety.DecisionDeny, last.Decision)
	require.Equal(t, "workspace_exec", last.ToolName)
	require.NotEmpty(t, last.RuleID)
}

func TestSafetyGuard_FlowAllowExecutes(t *testing.T) {
	t.Parallel()
	guard := safety.NewGuard(safety.WithAuditor(safety.NewMemoryAuditor()))

	var calledTool bool
	var gotArgs []byte
	p := NewFunctionCallResponseProcessor(false, nil)
	tl := &mockCallableTool{
		declaration: &tool.Declaration{Name: "workspace_exec"},
		callFn: func(_ context.Context, args []byte) (any, error) {
			calledTool = true
			gotArgs = append([]byte(nil), args...)
			return map[string]any{"ok": true}, nil
		},
	}
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicy(guard)),
	}
	args, err := json.Marshal(map[string]any{"command": "echo agent-safety-ok"})
	require.NoError(t, err)

	_, res, _, _, _, err := p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-allow-echo",
			Function: model.FunctionDefinitionParam{
				Name:      "workspace_exec",
				Arguments: args,
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	require.True(t, calledTool)
	require.JSONEq(t, string(args), string(gotArgs))
	require.JSONEq(t, `{"ok":true}`, string(mustJSON(res)))
}

func TestSafetyGuard_FlowAskBlocksWithoutExecute(t *testing.T) {
	t.Parallel()
	auditor := safety.NewMemoryAuditor()
	guard := safety.NewGuard(safety.WithAuditor(auditor))

	var calledTool bool
	p := NewFunctionCallResponseProcessor(false, nil)
	tl := &mockCallableTool{
		declaration: &tool.Declaration{Name: "workspace_exec"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			calledTool = true
			return map[string]any{"ok": true}, nil
		},
	}
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicy(guard)),
	}
	args, err := json.Marshal(map[string]any{"command": "npm install express"})
	require.NoError(t, err)

	_, res, _, _, _, err := p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-ask-npm",
			Function: model.FunctionDefinitionParam{
				Name:      "workspace_exec",
				Arguments: args,
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	require.False(t, calledTool, "ask must not execute until approved")

	body := mustJSON(res)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, tool.PermissionResultStatusApprovalRequired, payload["status"])
	require.NotEmpty(t, payload["reason"])

	events := auditor.Events()
	require.NotEmpty(t, events)
	require.Equal(t, safety.DecisionAsk, events[len(events)-1].Decision)
}
