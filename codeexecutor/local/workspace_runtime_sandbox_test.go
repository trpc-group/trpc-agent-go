//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type sandboxTestBackend struct {
	lastReq            codeexecutor.SandboxRequest
	lastInteractiveReq codeexecutor.SandboxInteractiveRequest
	result             codeexecutor.RunResult
	session            codeexecutor.ProgramSession
}

func (*sandboxTestBackend) Name() string { return "test_backend" }

func (*sandboxTestBackend) Capabilities() codeexecutor.SandboxBackendCapabilities {
	return codeexecutor.SandboxBackendCapabilities{}
}

func (*sandboxTestBackend) CanApply(codeexecutor.SandboxRequest) bool {
	return true
}

func (b *sandboxTestBackend) RunProgram(
	_ context.Context,
	req codeexecutor.SandboxRequest,
) (codeexecutor.RunResult, error) {
	b.lastReq = req
	return b.result, nil
}

func (*sandboxTestBackend) CanStartProgram(
	codeexecutor.SandboxInteractiveRequest,
) bool {
	return true
}

func (b *sandboxTestBackend) StartProgram(
	_ context.Context,
	req codeexecutor.SandboxInteractiveRequest,
) (codeexecutor.ProgramSession, error) {
	b.lastInteractiveReq = req
	return b.session, nil
}

type sandboxTestSession struct {
	id string
}

func (s *sandboxTestSession) ID() string { return s.id }

func (*sandboxTestSession) Poll(_ *int) codeexecutor.ProgramPoll {
	return codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusRunning}
}

func (*sandboxTestSession) Log(_, _ *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}

func (*sandboxTestSession) Write(string, bool) error { return nil }

func (*sandboxTestSession) Kill(time.Duration) error { return nil }

func (*sandboxTestSession) Close() error { return nil }

type promptApprovalDecider struct{}

func (promptApprovalDecider) DecideSandboxApproval(
	context.Context,
	codeexecutor.ApprovalRequest,
) (codeexecutor.ApprovalResult, error) {
	return codeexecutor.ApprovalResult{
		Action: codeexecutor.ApprovalActionPrompt,
		Rule:   "test_prompt",
	}, nil
}

func TestRuntimeRunProgram_UsesSandboxCoordinatorAndIntent(t *testing.T) {
	t.Parallel()

	backend := &sandboxTestBackend{
		result: codeexecutor.RunResult{
			Stdout:   "sandboxed",
			ExitCode: 0,
		},
	}
	rt := NewRuntimeWithOptions(
		"",
		WithRuntimeSandboxCoordinator(
			codeexecutor.NewSandboxCoordinator(
				codeexecutor.WithSandboxBackends(backend),
			),
		),
	)
	ws := codeexecutor.Workspace{
		ID:   "ws",
		Path: t.TempDir(),
	}
	ctx := codeexecutor.WithExecutionIntent(
		context.Background(),
		codeexecutor.ExecutionIntentWorkspaceExec,
	)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "echo"})
	require.NoError(t, err)
	require.Equal(t, "sandboxed", res.Stdout)
	require.Equal(
		t,
		codeexecutor.ExecutionIntentWorkspaceExec,
		backend.lastReq.Policy.Intent,
	)
	require.Equal(t, "local_process", backend.lastReq.Metadata["backend"])
}

func TestRuntimeRunProgram_PromptStopsExecution(t *testing.T) {
	t.Parallel()

	rt := NewRuntimeWithOptions(
		"",
		WithRuntimeSandboxCoordinator(
			codeexecutor.NewSandboxCoordinator(
				codeexecutor.WithSandboxApprovalDecider(
					promptApprovalDecider{},
				),
			),
		),
	)
	ws := codeexecutor.Workspace{
		ID:   "ws",
		Path: t.TempDir(),
	}

	_, err := rt.RunProgram(
		context.Background(),
		ws,
		codeexecutor.RunProgramSpec{Cmd: "echo"},
	)
	require.ErrorIs(t, err, codeexecutor.ErrSandboxApprovalRequired)
}

func TestRuntimeStartProgram_UsesSandboxCoordinatorAndIntent(t *testing.T) {
	t.Parallel()

	session := &sandboxTestSession{id: "sess-1"}
	backend := &sandboxTestBackend{session: session}
	rt := NewRuntimeWithOptions(
		"",
		WithRuntimeSandboxCoordinator(
			codeexecutor.NewSandboxCoordinator(
				codeexecutor.WithSandboxBackends(backend),
			),
		),
	)
	ws := codeexecutor.Workspace{
		ID:   "ws",
		Path: t.TempDir(),
	}
	ctx := codeexecutor.WithExecutionIntent(
		context.Background(),
		codeexecutor.ExecutionIntentWorkspaceExec,
	)

	got, err := rt.StartProgram(
		ctx,
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{Cmd: "sh"},
			TTY:            true,
		},
	)
	require.NoError(t, err)
	require.Same(t, session, got)
	require.Equal(
		t,
		codeexecutor.ExecutionIntentWorkspaceExec,
		backend.lastInteractiveReq.Policy.Intent,
	)
	require.True(t, backend.lastInteractiveReq.Spec.TTY)
	require.Equal(
		t,
		"interactive",
		backend.lastInteractiveReq.Metadata["session_mode"],
	)
}

func TestRuntimeStartProgram_PromptStopsExecution(t *testing.T) {
	t.Parallel()

	rt := NewRuntimeWithOptions(
		"",
		WithRuntimeSandboxCoordinator(
			codeexecutor.NewSandboxCoordinator(
				codeexecutor.WithSandboxApprovalDecider(
					promptApprovalDecider{},
				),
			),
		),
	)
	ws := codeexecutor.Workspace{
		ID:   "ws",
		Path: t.TempDir(),
	}

	_, err := rt.StartProgram(
		context.Background(),
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{Cmd: "sh"},
		},
	)
	require.ErrorIs(t, err, codeexecutor.ErrSandboxApprovalRequired)
}
