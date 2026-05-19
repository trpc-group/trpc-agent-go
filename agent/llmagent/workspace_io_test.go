//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestLLMAgent_Run_InstallsWorkspaceInContext verifies that callbacks
// receive a ready-to-use Workspace via WorkspaceFromContext when the
// agent is configured with a CodeExecutor. This is the only integration
// guarantee llmagent makes around the facade; sink composition,
// truncation, and budget policy are the caller's responsibility.
func TestLLMAgent_Run_InstallsWorkspaceInContext(t *testing.T) {
	exec := localexec.New()

	var captured *workspaceio.Workspace
	var captureOK bool

	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		captured, captureOK = workspaceio.WorkspaceFromContext(ctx)
		return nil, nil
	})

	a := New("agent",
		WithCodeExecutor(exec),
		WithAgentCallbacks(cb),
	)
	a.flow = &mockFlow{done: true}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-wsio-ctx"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "s1", AppName: "app", UserID: "user",
		}),
	)

	events, err := a.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.True(t, captureOK, "WorkspaceFromContext should resolve when WithCodeExecutor is configured")
	require.NotNil(t, captured)
}

// TestLLMAgent_Run_PreservesExistingWorkspaceInContext verifies the
// short-circuit branch in withWorkspace: when ctx already carries a
// Workspace (e.g. installed by an outer agent in a composition), Run
// must not overwrite it with a fresh facade.
func TestLLMAgent_Run_PreservesExistingWorkspaceInContext(t *testing.T) {
	exec := localexec.New()
	existing := workspaceio.New(exec, nil)

	var captured *workspaceio.Workspace
	var captureOK bool
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		captured, captureOK = workspaceio.WorkspaceFromContext(ctx)
		return nil, nil
	})

	a := New("agent",
		WithCodeExecutor(exec),
		WithAgentCallbacks(cb),
	)
	a.flow = &mockFlow{done: true}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-preserve-ws"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "s1", AppName: "app", UserID: "user",
		}),
	)

	ctx := workspaceio.WithWorkspace(context.Background(), existing)
	events, err := a.Run(ctx, inv)
	require.NoError(t, err)
	for range events {
	}

	require.True(t, captureOK, "BeforeAgent should still see a Workspace")
	require.Same(t, existing, captured,
		"withWorkspace must not overwrite an already-installed Workspace")
}

func TestLLMAgent_Run_WithoutCodeExecutor_NoWorkspaceInContext(t *testing.T) {
	var captureOK bool

	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		_, captureOK = workspaceio.WorkspaceFromContext(ctx)
		return nil, nil
	})

	a := New("agent", WithAgentCallbacks(cb))
	a.flow = &mockFlow{done: true}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-noexec"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "s1", AppName: "app", UserID: "user",
		}),
	)

	events, err := a.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.False(t, captureOK, "no Workspace should be installed when no executor is configured")
}
