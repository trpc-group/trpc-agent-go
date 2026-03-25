//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type testSandboxBackend struct {
	name    string
	accept  bool
	result  codeexecutor.RunResult
	runErr  error
	lastReq codeexecutor.SandboxRequest
}

func (b *testSandboxBackend) Name() string { return b.name }

func (b *testSandboxBackend) Capabilities() codeexecutor.SandboxBackendCapabilities {
	return codeexecutor.SandboxBackendCapabilities{ProcessIsolation: true}
}

func (b *testSandboxBackend) CanApply(
	_ codeexecutor.SandboxRequest,
) bool {
	return b.accept
}

func (b *testSandboxBackend) RunProgram(
	_ context.Context,
	req codeexecutor.SandboxRequest,
) (codeexecutor.RunResult, error) {
	b.lastReq = req
	return b.result, b.runErr
}

type testProgramSession struct {
	id string
}

func (s *testProgramSession) ID() string { return s.id }

func (*testProgramSession) Poll(_ *int) codeexecutor.ProgramPoll {
	return codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusRunning}
}

func (*testProgramSession) Log(_, _ *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}

func (*testProgramSession) Write(string, bool) error { return nil }

func (*testProgramSession) Kill(time.Duration) error { return nil }

func (*testProgramSession) Close() error { return nil }

type testInteractiveSandboxBackend struct {
	testSandboxBackend
	acceptInteractive bool
	session           codeexecutor.ProgramSession
	startErr          error
	lastInteractive   codeexecutor.SandboxInteractiveRequest
}

func (b *testInteractiveSandboxBackend) CanStartProgram(
	_ codeexecutor.SandboxInteractiveRequest,
) bool {
	return b.acceptInteractive
}

func (b *testInteractiveSandboxBackend) StartProgram(
	_ context.Context,
	req codeexecutor.SandboxInteractiveRequest,
) (codeexecutor.ProgramSession, error) {
	b.lastInteractive = req
	return b.session, b.startErr
}

type staticApprovalDecider struct {
	res codeexecutor.ApprovalResult
	err error
}

func (d staticApprovalDecider) DecideSandboxApproval(
	_ context.Context,
	_ codeexecutor.ApprovalRequest,
) (codeexecutor.ApprovalResult, error) {
	return d.res, d.err
}

func TestStaticSandboxPolicyResolver_UsesRequestIntentWhenUnset(t *testing.T) {
	t.Parallel()

	resolver := codeexecutor.StaticSandboxPolicyResolver{}
	pol, err := resolver.ResolveSandboxPolicy(
		context.Background(),
		codeexecutor.PolicyResolveRequest{
			Intent: codeexecutor.ExecutionIntentWorkspaceExec,
		},
	)
	require.NoError(t, err)
	require.Equal(t, codeexecutor.ExecutionIntentWorkspaceExec, pol.Intent)
}

func TestSandboxCoordinator_RunProgram_UsesSelectedBackend(t *testing.T) {
	t.Parallel()

	backend := &testSandboxBackend{
		name:   "local",
		accept: true,
		result: codeexecutor.RunResult{
			Stdout:   "ok",
			ExitCode: 0,
		},
	}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
		codeexecutor.WithSandboxPolicyResolver(
			codeexecutor.StaticSandboxPolicyResolver{
				Policy: codeexecutor.ExecutionPolicy{
					FileSystem: codeexecutor.FileSystemPolicy{
						Mode: codeexecutor.FileSystemAccessWorkspaceWrite,
					},
				},
			},
		),
	)

	res, err := c.RunProgram(
		context.Background(),
		codeexecutor.SandboxRunRequest{
			Intent:    codeexecutor.ExecutionIntentWorkspaceExec,
			Workspace: codeexecutor.Workspace{ID: "w1", Path: "/tmp/ws"},
			Spec: codeexecutor.RunProgramSpec{
				Cmd: "echo",
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ok", res.Stdout)
	require.Equal(
		t,
		codeexecutor.ExecutionIntentWorkspaceExec,
		backend.lastReq.Policy.Intent,
	)
}

func TestSandboxCoordinator_RunProgram_PromptStopsExecution(t *testing.T) {
	t.Parallel()

	backend := &testSandboxBackend{name: "local", accept: true}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
		codeexecutor.WithSandboxApprovalDecider(staticApprovalDecider{
			res: codeexecutor.ApprovalResult{
				Action: codeexecutor.ApprovalActionPrompt,
			},
		}),
	)

	_, err := c.RunProgram(
		context.Background(),
		codeexecutor.SandboxRunRequest{
			Intent: codeexecutor.ExecutionIntentWorkspaceExec,
		},
	)
	require.ErrorIs(t, err, codeexecutor.ErrSandboxApprovalRequired)
	require.Empty(t, backend.lastReq.Workspace.ID)
}

func TestSandboxCoordinator_RunProgram_DenyStopsExecution(t *testing.T) {
	t.Parallel()

	backend := &testSandboxBackend{name: "local", accept: true}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
		codeexecutor.WithSandboxApprovalDecider(staticApprovalDecider{
			res: codeexecutor.ApprovalResult{
				Action: codeexecutor.ApprovalActionDeny,
			},
		}),
	)

	_, err := c.RunProgram(
		context.Background(),
		codeexecutor.SandboxRunRequest{
			Intent: codeexecutor.ExecutionIntentWorkspaceExec,
		},
	)
	require.ErrorIs(t, err, codeexecutor.ErrSandboxApprovalDenied)
	require.Empty(t, backend.lastReq.Workspace.ID)
}

func TestFirstCompatibleSandboxBackendSelector_ReturnsFirstCompatible(t *testing.T) {
	t.Parallel()

	selector := codeexecutor.FirstCompatibleSandboxBackendSelector{}
	incompatible := &testSandboxBackend{name: "skip", accept: false}
	compatible := &testSandboxBackend{name: "use", accept: true}

	got, err := selector.SelectSandboxBackend(
		context.Background(),
		codeexecutor.SandboxRequest{},
		[]codeexecutor.SandboxBackend{incompatible, compatible},
	)
	require.NoError(t, err)
	require.Same(t, compatible, got)
}

func TestFirstCompatibleSandboxBackendSelector_ErrWhenNoBackend(t *testing.T) {
	t.Parallel()

	selector := codeexecutor.FirstCompatibleSandboxBackendSelector{}
	_, err := selector.SelectSandboxBackend(
		context.Background(),
		codeexecutor.SandboxRequest{},
		[]codeexecutor.SandboxBackend{
			&testSandboxBackend{name: "skip", accept: false},
		},
	)
	require.ErrorIs(t, err, codeexecutor.ErrNoSandboxBackend)
}

func TestSandboxCoordinator_StartProgram_UsesSelectedInteractiveBackend(t *testing.T) {
	t.Parallel()

	session := &testProgramSession{id: "sess-1"}
	backend := &testInteractiveSandboxBackend{
		testSandboxBackend: testSandboxBackend{
			name:   "local",
			accept: true,
		},
		acceptInteractive: true,
		session:           session,
	}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
	)

	got, err := c.StartProgram(
		context.Background(),
		codeexecutor.SandboxStartProgramRequest{
			Intent:    codeexecutor.ExecutionIntentWorkspaceExec,
			Workspace: codeexecutor.Workspace{ID: "w1", Path: "/tmp/ws"},
			Spec: codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{
					Cmd: "sh",
				},
				TTY: true,
			},
		},
	)
	require.NoError(t, err)
	require.Same(t, session, got)
	require.Equal(
		t,
		codeexecutor.ExecutionIntentWorkspaceExec,
		backend.lastInteractive.Policy.Intent,
	)
	require.True(t, backend.lastInteractive.Spec.TTY)
}

func TestSandboxCoordinator_StartProgram_PromptStopsExecution(t *testing.T) {
	t.Parallel()

	backend := &testInteractiveSandboxBackend{
		testSandboxBackend: testSandboxBackend{
			name:   "local",
			accept: true,
		},
		acceptInteractive: true,
		session:           &testProgramSession{id: "sess-1"},
	}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
		codeexecutor.WithSandboxApprovalDecider(staticApprovalDecider{
			res: codeexecutor.ApprovalResult{
				Action: codeexecutor.ApprovalActionPrompt,
			},
		}),
	)

	_, err := c.StartProgram(
		context.Background(),
		codeexecutor.SandboxStartProgramRequest{
			Intent: codeexecutor.ExecutionIntentWorkspaceExec,
			Spec: codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{Cmd: "sh"},
			},
		},
	)
	require.ErrorIs(t, err, codeexecutor.ErrSandboxApprovalRequired)
	require.Empty(t, backend.lastInteractive.Workspace.ID)
}

func TestFirstCompatibleSandboxBackendSelector_SelectsInteractiveBackend(t *testing.T) {
	t.Parallel()

	selector := codeexecutor.FirstCompatibleSandboxBackendSelector{}
	incompatible := &testInteractiveSandboxBackend{
		testSandboxBackend: testSandboxBackend{name: "skip"},
	}
	compatible := &testInteractiveSandboxBackend{
		testSandboxBackend: testSandboxBackend{name: "use"},
		acceptInteractive:  true,
	}

	got, err := selector.SelectSandboxInteractiveBackend(
		context.Background(),
		codeexecutor.SandboxInteractiveRequest{},
		[]codeexecutor.SandboxBackend{incompatible, compatible},
	)
	require.NoError(t, err)
	require.Same(t, compatible, got)
}

func TestSandboxCoordinator_RunProgram_PropagatesBackendError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("run failed")
	backend := &testSandboxBackend{name: "local", accept: true, runErr: wantErr}
	c := codeexecutor.NewSandboxCoordinator(
		codeexecutor.WithSandboxBackends(backend),
	)

	_, err := c.RunProgram(
		context.Background(),
		codeexecutor.SandboxRunRequest{
			Intent: codeexecutor.ExecutionIntentWorkspaceExec,
		},
	)
	require.ErrorIs(t, err, wantErr)
}
