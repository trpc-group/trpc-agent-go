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

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func TestMapControlledEgressSetupExit(t *testing.T) {
	t.Parallel()

	controlled := WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
		UnixPath: "/tmp/proxy.sock",
	})
	if err := mapControlledEgressSetupExit(controlled, controlledegress.ExitSetupFailed, true, false); !isKind(err, ErrSetupFailed) {
		t.Fatalf("exit 75 error = %v, want ErrSetupFailed", err)
	}
	if err := mapControlledEgressSetupExit(controlled, controlledegress.ExitUsageError, true, false); !isKind(err, ErrSetupFailed) {
		t.Fatalf("exit 2 error = %v, want ErrSetupFailed", err)
	}
	if err := mapControlledEgressSetupExit(controlled, 1, true, false); err != nil {
		t.Fatalf("user exit 1 must not map: %v", err)
	}
	if err := mapControlledEgressSetupExit(controlled, controlledegress.ExitSetupFailed, false, false); err != nil {
		t.Fatalf("user exit 75 must not map without setup marker: %v", err)
	}
	if err := mapControlledEgressSetupExit(controlled, controlledegress.ExitUsageError, false, false); err != nil {
		t.Fatalf("user exit 2 must not map without setup marker: %v", err)
	}

	restricted := WorkspaceWriteProfile()
	if err := mapControlledEgressSetupExit(restricted, controlledegress.ExitSetupFailed, true, false); err != nil {
		t.Fatalf("restricted must not map exit 75: %v", err)
	}
	enabled := WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	if err := mapControlledEgressSetupExit(enabled, controlledegress.ExitUsageError, true, false); err != nil {
		t.Fatalf("enabled must not map exit 2: %v", err)
	}
}

func TestControlledEgressSetupMarkerSpoofPreservesUserExit(t *testing.T) {
	t.Parallel()
	controlled := WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
		UnixPath: "/tmp/proxy.sock",
	})
	markers := &controlledEgressMarkerTracker{}
	setup := controlledegress.SetupErrorPrefix + " guest-controlled text"
	_, _ = markers.Write([]byte(setup[:5]))
	_, _ = markers.Write([]byte(setup[5:]))
	_, _ = markers.Write([]byte(controlledegress.UserExitPrefix + " 75\n"))
	if !markers.setupMarkerSeen() || !markers.userExitMarkerSeen() {
		t.Fatalf(
			"markers setup=%v user-exit=%v, want both",
			markers.setupMarkerSeen(),
			markers.userExitMarkerSeen(),
		)
	}
	if err := mapControlledEgressSetupExit(
		controlled,
		controlledegress.ExitSetupFailed,
		markers.setupMarkerSeen(),
		markers.userExitMarkerSeen(),
	); err != nil {
		t.Fatalf("trusted user-exit marker did not preserve user exit: %v", err)
	}
}

func TestProbeControlledEgressProxy(t *testing.T) {
	t.Parallel()
	missing := t.TempDir() + "/no-such.sock"
	if err := probeControlledEgressProxy(missing); !isKind(err, ErrSetupFailed) {
		t.Fatalf("missing sock error = %v, want ErrSetupFailed", err)
	}
	if err := probeControlledEgressProxy(""); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("empty path error = %v, want ErrPolicyViolation", err)
	}
}
