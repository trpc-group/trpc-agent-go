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
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// liveButNoRunnerExec implements codeexecutor.CodeExecutor +
// EngineProvider with FS + Manager but Runner=nil — mirroring real
// executors that intentionally omit ProgramRunner.
type liveButNoRunnerExec struct {
	eng codeexecutor.Engine
}

func (l *liveButNoRunnerExec) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (l *liveButNoRunnerExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func (l *liveButNoRunnerExec) Engine() codeexecutor.Engine { return l.eng }

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

// TestLLMAgent_Run_InstallsWorkspaceWhenRunnerNil pins withWorkspace's
// gate at "live workspace" (FS + Manager) rather than "workspace_exec"
// (FS + Manager + Runner). Executors that legitimately omit
// ProgramRunner can still serve Collect / PutFiles / StageInputs /
// SaveArtifact through the callback-side facade; RunProgram itself
// surfaces a targeted error when Runner is nil.
func TestLLMAgent_Run_InstallsWorkspaceWhenRunnerNil(t *testing.T) {
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, nil) // Manager + FS but no Runner
	exec := &liveButNoRunnerExec{eng: eng}

	var called, captureOK bool
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		called = true
		_, captureOK = workspaceio.WorkspaceFromContext(ctx)
		return nil, nil
	})

	a := New("agent", WithCodeExecutor(exec), WithAgentCallbacks(cb))
	a.flow = &mockFlow{done: true}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-norunner"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "s1", AppName: "app", UserID: "user",
		}),
	)

	events, err := a.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.True(t, called, "BeforeAgent must have run")
	require.True(t, captureOK,
		"withWorkspace must accept FS+Manager-only executors; RunProgram surfaces a targeted error when Runner is nil")
}

// TestLLMAgent_Run_HonorsRunOptionsCodeExecutorForWorkspace pins the
// contract that withWorkspace resolves the *effective* executor via
// codeExecutorForInvocation — same as workspace_exec / skill_run.
// Pre-fix, withWorkspace read a.codeExecutor unconditionally so when
// the executor only came from RunOptions, callbacks saw no Workspace
// even though the LLM tools used the override. Now they stay in sync.
func TestLLMAgent_Run_HonorsRunOptionsCodeExecutorForWorkspace(t *testing.T) {
	runExec := localexec.New()

	var called, captureOK bool
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		called = true
		_, captureOK = workspaceio.WorkspaceFromContext(ctx)
		return nil, nil
	})

	a := New("agent", WithAgentCallbacks(cb)) // no WithCodeExecutor
	a.flow = &mockFlow{done: true}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-runopts"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID: "s1", AppName: "app", UserID: "user",
		}),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithCodeExecutor(runExec),
		)),
	)

	events, err := a.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.True(t, called, "BeforeAgent must have run")
	require.True(t, captureOK,
		"withWorkspace must honor RunOptions.CodeExecutor for installing the facade")
}

func TestLLMAgent_Run_WithoutCodeExecutor_NoWorkspaceInContext(t *testing.T) {
	// captureOK defaults to false, so require.False alone would also pass
	// if the callback never ran. Pin "callback was invoked" explicitly via
	// `called` so the negative assertion can't quietly turn into a no-op
	// after future refactors of LLMAgent.Run.
	var called bool
	var captureOK bool

	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		called = true
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

	require.True(t, called, "BeforeAgent callback must run for the negative assertion below to be meaningful")
	require.False(t, captureOK, "no Workspace should be installed when no executor is configured")
}
