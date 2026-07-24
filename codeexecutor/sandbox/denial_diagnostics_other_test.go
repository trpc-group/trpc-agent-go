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
	"testing"
	"time"
)

func TestNonDarwinDenialDiagnosticsStubs(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if err := rt.ensureDenialMonitor(); err != nil {
		t.Fatalf("ensureDenialMonitor: %v", err)
	}
	caps := rt.DiagnosticsCapability()
	if caps != (DiagnosticsCapability{}) {
		t.Fatalf("diagnosticsCapabilityForPlatform = %#v, want zero value", caps)
	}
	run := rt.newSandboxDenialRun(WorkspaceWriteProfile())
	if run != (sandboxDenialRun{}) {
		t.Fatalf("newSandboxDenialRun = %#v, want zero value", run)
	}
	if got := rt.sandboxDenialRunForCollecting(WorkspaceWriteProfile()); got != (sandboxDenialRun{}) {
		t.Fatalf("sandboxDenialRunForCollecting = %#v, want zero value", got)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatalf("sandboxDenialCollectingReady = true, want false")
	}
	denials := rt.collectSandboxDenials("tag", "/bin/cat", time.Millisecond)
	if denials != nil {
		t.Fatalf("collectSandboxDenials = %#v, want nil", denials)
	}

	rt.setDenialFilter(DenialFilter{
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"file-read-data"},
		}},
	})
}
