//go:build !darwin

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
	"time"
)

func (r *Runtime) initDenialDiagnosticsState() {}

func (r *Runtime) setDenialFilter(filter DenialFilter) {
	_ = r
	_ = filter
}

func (r *Runtime) diagnosticsCapabilityForPlatform() DiagnosticsCapability {
	return DiagnosticsCapability{}
}

func (r *Runtime) newSandboxDenialRun(
	profile PermissionProfile,
) sandboxDenialRun {
	_ = r
	_ = profile
	return sandboxDenialRun{}
}

func (r *Runtime) ensureDenialMonitor(ctx context.Context) error {
	_ = r
	_ = ctx
	return nil
}

func (r *Runtime) collectSandboxDenials(
	ctx context.Context,
	runTag string,
	droppedAtStart uint64,
	cmd string,
	settleTimeout time.Duration,
) ([]Denial, bool) {
	_ = r
	_ = ctx
	_ = runTag
	_ = droppedAtStart
	_ = cmd
	_ = settleTimeout
	return nil, false
}

func (r *Runtime) sandboxDenialRunForCollecting(
	profile PermissionProfile,
) sandboxDenialRun {
	_ = r
	_ = profile
	return sandboxDenialRun{}
}

func (r *Runtime) sandboxDenialCollectingReady() bool {
	return false
}

func (r *Runtime) closeDenialDiagnostics() error {
	return nil
}
