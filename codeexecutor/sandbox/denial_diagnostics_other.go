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

func (r *Runtime) ensureDenialMonitor() error {
	return nil
}

func (r *Runtime) collectSandboxDenials(
	runTag string,
	cmd string,
	settleTimeout time.Duration,
) []Denial {
	_ = r
	_ = runTag
	_ = cmd
	_ = settleTimeout
	return nil
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
