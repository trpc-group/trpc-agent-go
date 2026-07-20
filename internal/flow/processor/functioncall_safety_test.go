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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestSafetyGuard_PreventsToolExecutionThroughFramework verifies that
// the tool/safety Guard, when registered as a ToolPermissionPolicy,
// prevents CallableTool.Call from running when it returns Deny. This
// is the cross-module proof required by the plan's Task 6: it tests
// the real framework execution order, not just the adapter return value.
func TestSafetyGuard_PreventsToolExecutionThroughFramework(t *testing.T) {
	// Use a policy with audit disabled to avoid creating audit files
	// in the test CWD.
	policy := safety.DefaultPolicy()
	policy.Audit.Path = ""
	policy.Audit.Required = false
	guard, err := safety.NewGuard(safety.WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

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
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
			guard.CheckToolPermission,
		)),
	}

	_, res, _, _, _, err := p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-safety-deny",
			Function: model.FunctionDefinitionParam{
				Name:      "workspace_exec",
				Arguments: []byte(`{"command":"rm -rf /"}`),
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	// The tool must NOT have been called.
	require.False(t, calledTool,
		"CallableTool.Call must not run when the safety guard denies")
	// The result must be a structured denial.
	require.NotNil(t, res)
	require.Contains(t, string(mustJSON(res)), "denied")
}

// TestSafetyGuard_AllowsSafeToolThroughFramework verifies that a safe
// command passes through the framework and the tool IS called.
func TestSafetyGuard_AllowsSafeToolThroughFramework(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.Audit.Path = ""
	policy.Audit.Required = false
	guard, err := safety.NewGuard(safety.WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

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
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
			guard.CheckToolPermission,
		)),
	}

	_, _, _, _, _, err = p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-safety-allow",
			Function: model.FunctionDefinitionParam{
				Name:      "workspace_exec",
				Arguments: []byte(`{"command":"go test ./..."}`),
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	require.True(t, calledTool,
		"CallableTool.Call must run when the safety guard allows")
}
