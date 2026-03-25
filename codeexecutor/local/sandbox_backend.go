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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var _ codeexecutor.SandboxBackend = (*SandboxBackend)(nil)
var _ codeexecutor.SandboxInteractiveBackend = (*SandboxBackend)(nil)

// SandboxBackend adapts the existing local Runtime to the draft sandbox backend
// interface without changing current execution behavior.
type SandboxBackend struct {
	rt *Runtime
}

// NewSandboxBackend creates a backend backed by the provided local runtime.
func NewSandboxBackend(rt *Runtime) *SandboxBackend {
	return &SandboxBackend{rt: rt}
}

// SandboxBackend returns a draft sandbox backend view of the runtime.
func (r *Runtime) SandboxBackend() codeexecutor.SandboxBackend {
	return NewSandboxBackend(r)
}

// SandboxBackend returns a draft sandbox backend view of the code executor's
// underlying workspace runtime.
func (e *CodeExecutor) SandboxBackend() codeexecutor.SandboxBackend {
	return NewSandboxBackend(e.ensureWS())
}

// Name identifies this backend.
func (*SandboxBackend) Name() string { return "local_process" }

// Capabilities describes what the current local backend can guarantee today.
func (*SandboxBackend) Capabilities() codeexecutor.SandboxBackendCapabilities {
	return codeexecutor.SandboxBackendCapabilities{
		ProcessIsolation:    false,
		ContainerIsolation:  false,
		NetworkIsolation:    false,
		ProtectedSubpaths:   false,
		InteractiveSessions: true,
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

// RunProgram delegates to the existing local runtime.
func (b *SandboxBackend) RunProgram(
	ctx context.Context,
	req codeexecutor.SandboxRequest,
) (codeexecutor.RunResult, error) {
	return b.rt.runProgramDirect(ctx, req.Workspace, req.Spec)
}

// CanStartProgram reports whether the backend can handle the interactive
// request shape.
func (b *SandboxBackend) CanStartProgram(
	req codeexecutor.SandboxInteractiveRequest,
) bool {
	return b != nil &&
		b.rt != nil &&
		req.Workspace.Path != "" &&
		strings.TrimSpace(req.Spec.Cmd) != ""
}

// StartProgram delegates to the existing local interactive runtime.
func (b *SandboxBackend) StartProgram(
	ctx context.Context,
	req codeexecutor.SandboxInteractiveRequest,
) (codeexecutor.ProgramSession, error) {
	return b.rt.startProgramDirect(ctx, req.Workspace, req.Spec)
}
