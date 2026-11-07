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
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNetworkPolicyModes(t *testing.T) {
	restricted, err := resolveNetworkPolicy(WorkspaceWriteProfile())
	if err != nil || restricted.mode != NetworkRestricted || !restricted.isolateNetwork {
		t.Fatalf("restricted = %+v err=%v", restricted, err)
	}
	enabled, err := resolveNetworkPolicy(
		WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled}),
	)
	if err != nil || enabled.mode != NetworkEnabled || enabled.isolateNetwork {
		t.Fatalf("enabled = %+v err=%v", enabled, err)
	}
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	controlled, err := resolveNetworkPolicy(
		WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
			UnixPath: sock,
		}),
	)
	if err != nil || controlled.mode != NetworkControlled || !controlled.isolateNetwork {
		t.Fatalf("controlled = %+v err=%v", controlled, err)
	}
	if controlled.unixPath != sock || controlled.relayPort == 0 {
		t.Fatalf("controlled endpoint = %+v", controlled)
	}
}

func TestControlledEgressRequiresAbsoluteUnixPath(t *testing.T) {
	_, err := resolveNetworkPolicy(
		WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
			UnixPath: "relative.sock",
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want absolute path failure", err)
	}
}

func TestUnknownNetworkModeFailsClosed(t *testing.T) {
	_, err := resolveNetworkPolicy(PermissionProfile{
		typ:     profileManaged,
		network: NetworkPolicy{Mode: "weird"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported network mode") {
		t.Fatalf("err = %v, want unsupported mode", err)
	}
}

func TestDisabledProfileRejectsControlled(t *testing.T) {
	err := validateProfileNetworkPolicy(
		DangerFullAccessProfile().WithControlledEgressProxy(ControlledEgressProxy{
			UnixPath: filepath.Join(t.TempDir(), "proxy.sock"),
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "disabled sandbox") {
		t.Fatalf("err = %v, want disabled sandbox rejection", err)
	}
}
