//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

package processor

// End-to-end integration test for issue #2002 acceptance criterion 7: the
// tool/safety scanner, wired as the agent's tool.PermissionPolicy through
// functioncall.go, must stop a dangerous command before the tool executes and
// leave an auditable event behind.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type safetyFlowTool struct {
	*mockCallableTool
	calls int
}

func (m *safetyFlowTool) callFnCount() int { return m.calls }

func newSafetyFlow(t *testing.T) (*FunctionCallResponseProcessor, *agent.Invocation, *safetyFlowTool, *safety.Scanner) {
	return newSafetyFlowWithTool(t, nil, "workspace_exec")
}

// newSafetyFlowWithPolicy builds the flow with an explicit policy (nil uses
// DefaultPolicy).
func newSafetyFlowWithPolicy(t *testing.T, policy *safety.Policy) (*FunctionCallResponseProcessor, *agent.Invocation, *safetyFlowTool, *safety.Scanner) {
	return newSafetyFlowWithTool(t, policy, "workspace_exec")
}

// newSafetyFlowWithTool builds the flow with an explicit policy and tool name
// (host-executing tools must keep the hostexec boundary through the
// permission path).
func newSafetyFlowWithTool(t *testing.T, policy *safety.Policy, toolName string) (*FunctionCallResponseProcessor, *agent.Invocation, *safetyFlowTool, *safety.Scanner) {
	t.Helper()
	scanner := safety.NewScanner(policy)
	mock := &safetyFlowTool{}
	mock.mockCallableTool = &mockCallableTool{
		declaration: &tool.Declaration{Name: toolName},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			mock.calls++
			return map[string]any{"ok": true}, nil
		},
	}
	p := NewFunctionCallResponseProcessor(false, tool.NewCallbacks())
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(agent.WithToolPermissionPolicyFunc(
			func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return scanner.CheckToolPermission(ctx, req)
			},
		)),
	}
	return p, inv, mock, scanner
}

func runSafetyFlow(
	t *testing.T,
	p *FunctionCallResponseProcessor,
	inv *agent.Invocation,
	mock *safetyFlowTool,
	args string,
) (any, error) {
	t.Helper()
	_, res, _, _, _, err := p.executeToolWithCallbacks(
		context.Background(),
		inv,
		model.ToolCall{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      mock.declaration.Name,
				Arguments: []byte(args),
			},
		},
		mock,
		nil,
	)
	return res, err
}

// TestSafetyFlow_DangerousCommandSkipped: a command denied by the scanner is
// never executed, the framework receives a permission result, and the scanner
// recorded an auditable denial event.
func TestSafetyFlow_DangerousCommandSkipped(t *testing.T) {
	p, inv, mock, scanner := newSafetyFlow(t)

	res, err := runSafetyFlow(t, p, inv, mock, `{"command":"rm -rf /"}`)
	require.NoError(t, err)
	require.NotNil(t, res, "a permission result must be produced for a denial")
	assert.Zero(t, mock.callFnCount(), "denied tool must not execute")

	var pr struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	b, err := json.Marshal(res)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &pr))
	assert.Equal(t, "denied", pr.Status)
	assert.NotEmpty(t, pr.Reason, "denial must carry the scanner's reason")

	events := scanner.Auditor().Events()
	require.Len(t, events, 1, "denial must be audited")
	assert.Equal(t, safety.DecisionDeny, events[0].Decision)
	assert.True(t, events[0].Intercepted)
	assert.Equal(t, "dangerous_cmd_001", events[0].RuleID)
}

// TestSafetyFlow_SafeCommandExecutes: the allow path passes the permission
// gate and reaches the tool.
func TestSafetyFlow_SafeCommandExecutes(t *testing.T) {
	p, inv, mock, scanner := newSafetyFlow(t)

	res, err := runSafetyFlow(t, p, inv, mock, `{"command":"echo hello"}`)
	require.NoError(t, err)
	assert.Equal(t, 1, mock.callFnCount(), "safe tool must execute")
	// On allow the framework returns the tool result, not a permission result.
	assert.Equal(t, map[string]any{"ok": true}, res)

	events := scanner.Auditor().Events()
	require.Len(t, events, 1)
	assert.Equal(t, safety.DecisionAllow, events[0].Decision)
}

// TestSafetyFlow_HostExecLongSessionAsked: an ask verdict also stops execution
// for human review.
func TestSafetyFlow_HostExecLongSessionAsked(t *testing.T) {
	// Disable the allowed-commands gate so the hostexec long-session check is
	// the one driving the verdict; the tool name carries the hostexec backend.
	policy := safety.DefaultPolicy()
	policy.AllowedCommands = nil
	p, inv, mock, scanner := newSafetyFlowWithTool(t, policy, "host_exec")

	res, err := runSafetyFlow(t, p, inv, mock, `{"command":"tail -f /var/log/app.log"}`)
	require.NoError(t, err)
	require.NotNil(t, res, "an ask verdict must produce a permission result")
	assert.Zero(t, mock.callFnCount(), "ask verdict must not execute the tool")

	events := scanner.Auditor().Events()
	require.Len(t, events, 1)
	assert.Equal(t, safety.DecisionAsk, events[0].Decision)
	assert.Equal(t, "hostexec_long_session", events[0].RuleID)
}

// TestSafetyFlow_ForeignCodeAsked: codeexec-style requests that cannot be
// structurally parsed fail closed through the flow.
func TestSafetyFlow_ForeignCodeAsked(t *testing.T) {
	p, inv, mock, scanner := newSafetyFlow(t)

	res, err := runSafetyFlow(t, p, inv, mock, `{"code":"print('hello')","language":"python"}`)
	require.NoError(t, err)
	require.NotNil(t, res, "unscannable code must produce a permission result")
	assert.Zero(t, mock.callFnCount(), "unscannable code must not execute")

	events := scanner.Auditor().Events()
	require.Len(t, events, 1)
	assert.Equal(t, safety.DecisionAsk, events[0].Decision)
	assert.Equal(t, "foreign_code_unscanned", events[0].RuleID)
}

// TestSafetyFlow_TildeTraversalDenied: the ~user traversal fix works through
// the real permission path.
func TestSafetyFlow_TildeTraversalDenied(t *testing.T) {
	p, inv, mock, scanner := newSafetyFlow(t)

	res, err := runSafetyFlow(t, p, inv, mock, `{"command":"cat ~root/../etc/shadow"}`)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Zero(t, mock.callFnCount())

	events := scanner.Auditor().Events()
	require.Len(t, events, 1)
	assert.Equal(t, safety.DecisionDeny, events[0].Decision)
}
