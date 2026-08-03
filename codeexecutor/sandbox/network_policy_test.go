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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestResolveNetworkPolicyModes(t *testing.T) {
	t.Parallel()

	restricted, err := resolveNetworkPolicy(
		PermissionProfile{network: NetworkPolicy{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if restricted.mode != NetworkRestricted || !restricted.isolateNetwork {
		t.Fatalf("empty plan = %#v, want restricted+isolate", restricted)
	}

	enabled, err := resolveNetworkPolicy(PermissionProfile{
		network: NetworkPolicy{Mode: NetworkEnabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enabled.mode != NetworkEnabled || enabled.isolateNetwork {
		t.Fatalf("enabled plan = %#v", enabled)
	}

	_, err = resolveNetworkPolicy(PermissionProfile{
		network: NetworkPolicy{Mode: "weird"},
	})
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("unknown mode error = %v, want ErrPolicyViolation", err)
	}

	_, err = resolveNetworkPolicy(PermissionProfile{
		network: NetworkPolicy{Mode: NetworkControlled},
	})
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("controlled without endpoint error = %v", err)
	}

	controlled, err := resolveNetworkPolicy(
		WorkspaceWriteProfile().WithControlledEgressProxy(
			ControlledEgressProxy{UnixPath: "/tmp/proxy.sock"},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if controlled.mode != NetworkControlled ||
		!controlled.isolateNetwork ||
		controlled.unixPath != "/tmp/proxy.sock" {
		t.Fatalf("controlled plan = %#v", controlled)
	}

	for _, port := range []int{-1, 65536} {
		_, err := resolveNetworkPolicy(
			WorkspaceWriteProfile().WithControlledEgressProxy(
				ControlledEgressProxy{
					UnixPath:  "/tmp/proxy.sock",
					RelayPort: port,
				},
			),
		)
		if !isKind(err, ErrPolicyViolation) {
			t.Fatalf(
				"relay port %d error = %v, want ErrPolicyViolation",
				port,
				err,
			)
		}
	}

	restrictedAgain := WorkspaceWriteProfile().
		WithControlledEgressProxy(
			ControlledEgressProxy{UnixPath: "/tmp/stale.sock"},
		).
		WithNetworkPolicy(NetworkPolicy{Mode: NetworkRestricted})
	if restrictedAgain.controlledEgress != (ControlledEgressProxy{}) {
		t.Fatalf(
			"restricted profile retained controlled config: %#v",
			restrictedAgain.controlledEgress,
		)
	}
}

func TestPrepareRunUnknownNetworkMode(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(func() PermissionProfile {
			p := WorkspaceWriteProfile()
			p.network.Mode = NetworkMode("weird")
			return p
		}()),
	)
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"prep-unknown-net",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.prepareRun(
		context.Background(),
		ws,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
	)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("prepareRun error = %v, want ErrPolicyViolation", err)
	}
}

func TestDescribeControlledNetworkNotAllowed(t *testing.T) {
	rt := NewRuntime(WithPermissionProfile(
		WorkspaceWriteProfile().WithControlledEgressProxy(
			ControlledEgressProxy{UnixPath: "/tmp/proxy.sock"},
		),
	))
	if rt.Describe().NetworkAllowed {
		t.Fatal("controlled must report NetworkAllowed=false")
	}
}
