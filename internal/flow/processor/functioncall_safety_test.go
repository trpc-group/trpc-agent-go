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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type safetyTestPluginManager struct {
	callbacks *tool.Callbacks
}

func (*safetyTestPluginManager) AgentCallbacks() *agent.Callbacks {
	return nil
}

func (*safetyTestPluginManager) ModelCallbacks() *model.Callbacks {
	return nil
}

func (m *safetyTestPluginManager) ToolCallbacks() *tool.Callbacks {
	return m.callbacks
}

func (*safetyTestPluginManager) OnEvent(
	_ context.Context,
	_ *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	return e, nil
}

func (*safetyTestPluginManager) Close(context.Context) error {
	return nil
}

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
				Arguments: []byte(`{"command":"rm -rf /","timeout":10}`),
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
				Arguments: []byte(`{"command":"go test ./...","timeout":10}`),
			},
		},
		tl,
		nil,
	)
	require.NoError(t, err)
	require.True(t, calledTool,
		"CallableTool.Call must run when the safety guard allows")
}

// TestAfterToolFinalizerResultPreservedOnCallbackError verifies that the
// flow engine propagates safety-critical finalizer output even when a
// regular after-tool callback fails.
func TestAfterToolFinalizerResultPreservedOnCallbackError(t *testing.T) {
	callbackErr := errors.New("callback failed")
	finalized := map[string]any{"output": "[REDACTED]"}
	type contextKey struct{}

	newCallbacks := func(t *testing.T) *tool.Callbacks {
		callbacks := tool.NewCallbacks()
		callbacks.RegisterAfterTool(func(
			context.Context,
			*tool.AfterToolArgs,
		) (*tool.AfterToolResult, error) {
			return nil, callbackErr
		})
		callbacks.RegisterAfterToolFinalizer(func(
			ctx context.Context,
			args *tool.AfterToolArgs,
		) (*tool.AfterToolResult, error) {
			require.Equal(t, "raw secret", args.Result)
			return &tool.AfterToolResult{
				Context:           context.WithValue(ctx, contextKey{}, "finalized"),
				CustomResult:      finalized,
				SkipSummarization: true,
			}, nil
		})
		return callbacks
	}

	toolCall := model.ToolCall{
		ID: "call-finalizer",
		Function: model.FunctionDefinitionParam{
			Name:      "test-tool",
			Arguments: []byte(`{}`),
		},
	}
	declaration := &tool.Declaration{Name: "test-tool"}

	t.Run("local callbacks", func(t *testing.T) {
		p := NewFunctionCallResponseProcessor(false, newCallbacks(t))
		ctx, result, skip, err := p.runAfterToolCallbacks(
			context.Background(),
			toolCall,
			declaration,
			"raw secret",
			nil,
		)
		require.ErrorIs(t, err, callbackErr)
		require.Equal(t, finalized, result)
		require.True(t, skip)
		require.Equal(t, "finalized", ctx.Value(contextKey{}))
	})

	t.Run("plugin callbacks", func(t *testing.T) {
		p := NewFunctionCallResponseProcessor(false, nil)
		invocation := &agent.Invocation{
			Plugins: &safetyTestPluginManager{callbacks: newCallbacks(t)},
		}
		ctx, result, override, skip, err := p.runAfterToolPluginCallbacks(
			context.Background(),
			invocation,
			toolCall,
			declaration,
			"raw secret",
			nil,
		)
		require.ErrorIs(t, err, callbackErr)
		require.Equal(t, finalized, result)
		require.True(t, override)
		require.True(t, skip)
		require.Equal(t, "finalized", ctx.Value(contextKey{}))
	})

	t.Run("tool execution boundary", func(t *testing.T) {
		p := NewFunctionCallResponseProcessor(false, newCallbacks(t))
		tl := &mockCallableTool{
			declaration: declaration,
			callFn: func(context.Context, []byte) (any, error) {
				return "raw secret", nil
			},
		}
		ctx, result, _, _, skip, err := p.executeToolWithCallbacks(
			context.Background(),
			&agent.Invocation{},
			toolCall,
			tl,
			nil,
		)
		require.ErrorIs(t, err, callbackErr)
		require.Equal(t, finalized, result)
		require.True(t, skip)
		require.Equal(t, "finalized", ctx.Value(contextKey{}))
	})
}
