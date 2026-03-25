//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type sandboxTestBackend struct {
	lastReq codeexecutor.SandboxRequest
	result  codeexecutor.RunResult
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

func TestWorkspaceRuntimeRunProgram_UsesSandboxCoordinatorAndIntent(t *testing.T) {
	t.Parallel()

	backend := &sandboxTestBackend{
		result: codeexecutor.RunResult{
			Stdout:   "sandboxed",
			ExitCode: 0,
		},
	}
	rt := &workspaceRuntime{
		sandbox: codeexecutor.NewSandboxCoordinator(
			codeexecutor.WithSandboxBackends(backend),
		),
	}
	ctx := codeexecutor.WithExecutionIntent(
		context.Background(),
		codeexecutor.ExecutionIntentWorkspaceExec,
	)

	res, err := rt.RunProgram(
		ctx,
		codeexecutor.Workspace{ID: "ws", Path: "/tmp/ws"},
		codeexecutor.RunProgramSpec{Cmd: "echo"},
	)
	require.NoError(t, err)
	require.Equal(t, "sandboxed", res.Stdout)
	require.Equal(
		t,
		codeexecutor.ExecutionIntentWorkspaceExec,
		backend.lastReq.Policy.Intent,
	)
	require.Equal(t, "container_runtime", backend.lastReq.Metadata["backend"])
}
