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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func TestMapControlledEgressSetupExit(t *testing.T) {
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
		UnixPath: "/tmp/proxy.sock",
	})
	if err := mapControlledEgressSetupExit(profile, controlledegress.ExitSetupFailed, true); err == nil {
		t.Fatal("expected setup failure mapping")
	}
	if err := mapControlledEgressSetupExit(WorkspaceWriteProfile(), 75, true); err != nil {
		t.Fatalf("restricted should ignore relay markers: %v", err)
	}
}

func TestControlledEgressEnvReplacesProxyVars(t *testing.T) {
	env := applyControlledEgressEnv(
		[]string{"HTTP_PROXY=http://evil", "FOO=bar", "NO_PROXY=*"},
		resolvedNetworkPolicy{relayPort: 17923},
	)
	joined := strings.Join(env, ",")
	if strings.Contains(joined, "evil") || strings.Contains(joined, "NO_PROXY") {
		t.Fatalf("proxy env not replaced: %v", env)
	}
	if !strings.Contains(joined, "HTTP_PROXY=http://127.0.0.1:17923") {
		t.Fatalf("missing injected proxy: %v", env)
	}
	if strings.Contains(joined, "TRPC_AGENT_CONTROLLED_EGRESS_FD") {
		t.Fatalf("obsolete mux fd env was retained: %v", env)
	}
}

func TestControlledEgressUserStderrCannotForgeSetupMarker(t *testing.T) {
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(
		ControlledEgressProxy{UnixPath: "/host/proxy.sock"},
	)
	tracker := newControlledEgressMarkerTracker("trusted-token")
	_, _ = tracker.Write([]byte(
		controlledegress.SetupErrorPrefix + " text emitted by the workload\n",
	))
	err := mapControlledEgressSetupExit(
		profile,
		controlledegress.ExitSetupFailed,
		tracker.setupMarkerSeen(),
	)
	if err != nil {
		t.Fatalf("user-controlled stderr was misclassified: %v", err)
	}
}

func TestControlledEgressTrustedSetupMarkerIsClassified(t *testing.T) {
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(
		ControlledEgressProxy{UnixPath: "/host/proxy.sock"},
	)
	tracker := newControlledEgressMarkerTracker("trusted-token")
	_, _ = tracker.Write([]byte(
		controlledegress.SetupErrorPrefix + "trusted-token: relay failed\n",
	))
	err := mapControlledEgressSetupExit(
		profile,
		controlledegress.ExitSetupFailed,
		tracker.setupMarkerSeen(),
	)
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("trusted setup marker was not classified: %v", err)
	}
}
