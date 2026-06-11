//go:build !linux

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"runtime"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func backendCapabilities(backend BackendType, profile PermissionProfile) backendCapabilitiesInfo {
	_ = backend
	_ = profile
	return backendCapabilitiesInfo{
		OSSandbox:          false,
		PTY:                false,
		Stdin:              true,
		NetworkIsolation:   false,
		DenyReadGlob:       false,
		Snapshot:           false,
		Ports:              false,
		ExternalPathGrants: false,
		ProtectedPathMasks: false,
		PerCommandGrants:   true,
	}
}

func (r *Runtime) osSandboxCommand(
	ctx context.Context,
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
) (*exec.Cmd, string, commandCleanup, error) {
	_ = r
	_ = ctx
	_ = profile
	_ = ws
	_ = cwd
	_ = env
	_ = spec
	return nil, runtime.GOOS, nil, backendError(
		ErrUnsupportedBackend,
		runtime.GOOS,
		errors.New("managed OS sandbox backend is not implemented for this platform"),
	)
}
