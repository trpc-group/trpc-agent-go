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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var _ codeexecutor.SandboxBackend = (*SandboxBackend)(nil)

// SandboxBackend adapts the existing container workspace runtime to the draft
// sandbox backend interface.
type SandboxBackend struct {
	rt              *workspaceRuntime
	networkIsolated bool
}

// NewSandboxBackend creates a backend backed by the provided container executor.
func NewSandboxBackend(exec *CodeExecutor) (*SandboxBackend, error) {
	if exec == nil {
		return nil, nil
	}
	rt, err := exec.ensureWS()
	if err != nil {
		return nil, err
	}
	return newSandboxBackendFromRuntime(
		rt,
		exec.hostConfig.NetworkMode == "none",
	), nil
}

func newSandboxBackendFromRuntime(
	rt *workspaceRuntime,
	networkIsolated bool,
) *SandboxBackend {
	return &SandboxBackend{
		rt:              rt,
		networkIsolated: networkIsolated,
	}
}

// SandboxBackend returns a draft sandbox backend view of the container executor.
func (c *CodeExecutor) SandboxBackend() (codeexecutor.SandboxBackend, error) {
	return NewSandboxBackend(c)
}

// Name identifies this backend.
func (*SandboxBackend) Name() string { return "container_runtime" }

// Capabilities describes what the current container backend can guarantee today.
func (b *SandboxBackend) Capabilities() codeexecutor.SandboxBackendCapabilities {
	return codeexecutor.SandboxBackendCapabilities{
		ProcessIsolation:    false,
		ContainerIsolation:  true,
		NetworkIsolation:    b != nil && b.networkIsolated,
		ProtectedSubpaths:   false,
		InteractiveSessions: false,
		PermissionOverlays:  false,
	}
}

// CanApply reports whether the backend can handle the request shape.
func (b *SandboxBackend) CanApply(req codeexecutor.SandboxRequest) bool {
	return b != nil &&
		b.rt != nil &&
		req.Workspace.Path != "" &&
		strings.TrimSpace(req.Spec.Cmd) != ""
}

// RunProgram delegates to the existing container runtime.
func (b *SandboxBackend) RunProgram(
	ctx context.Context,
	req codeexecutor.SandboxRequest,
) (codeexecutor.RunResult, error) {
	return b.rt.runProgramDirect(ctx, req.Workspace, req.Spec)
}
