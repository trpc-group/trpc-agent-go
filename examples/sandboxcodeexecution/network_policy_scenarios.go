//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

const (
	networkPolicyRestrictedMarker            = "NETWORK_POLICY_RESTRICTED_OK"
	networkPolicyEnabledMarker               = "NETWORK_POLICY_ENABLED_OK"
	networkPolicyAdditionalPermissionsMarker = "NETWORK_POLICY_ADDITIONAL_PERMISSIONS_OK"
	networkPolicyAgentMarker                 = "NETWORK_POLICY_AGENT_ENFORCEMENT_OK"
	networkPolicyDeniedProbe                 = "NETWORK_POLICY_PROBE_DENIED"
	networkPolicyConnectedProbe              = "NETWORK_POLICY_PROBE_CONNECTED"
)

const networkPolicyProbeScript = `
import socket

s = socket.socket()
s.settimeout(1)
try:
    s.connect(("1.1.1.1", 80))
    print("NETWORK_POLICY_PROBE_CONNECTED")
except OSError:
    print("NETWORK_POLICY_PROBE_DENIED")
finally:
    s.close()
`

func runNetworkRestricted(ctx context.Context, cfg config) error {
	return runNetworkPolicyRestricted(ctx, cfg)
}

func runNetworkPolicyRestricted(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "network-policy-restricted", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := expectNetworkDenied(ctx, rt, ws); err != nil {
		return err
	}
	fmt.Println(networkPolicyRestrictedMarker)
	return nil
}

func runNetworkPolicyEnabled(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNetworkPolicy(
		sandbox.NetworkPolicy{Mode: sandbox.NetworkEnabled},
	)
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "network-policy-enabled", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := expectNetworkConnected(ctx, rt, ws); err != nil {
		return err
	}
	fmt.Println(networkPolicyEnabledMarker)
	return nil
}

func runNetworkPolicyAdditionalPermissions(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "network-policy-additional-permissions", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := expectNetworkDenied(ctx, rt, ws); err != nil {
		return err
	}
	grantCtx := sandbox.WithAdditionalPermissions(ctx, sandbox.AdditionalPermissions{
		Network: &sandbox.NetworkPolicy{Mode: sandbox.NetworkEnabled},
	})
	if err := expectNetworkConnected(grantCtx, rt, ws); err != nil {
		return err
	}
	if err := expectNetworkDenied(ctx, rt, ws); err != nil {
		return err
	}
	fmt.Println(networkPolicyAdditionalPermissionsMarker)
	return nil
}

func runNetworkPolicyAgentEnforcement(ctx context.Context, cfg config) error {
	h, err := newAgentToolHarness(ctx, cfg, sandbox.WorkspaceWriteProfile(), nil)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "network-policy-agent-enforcement", `Use workspace_exec to verify sandbox network policy behavior.

Run Python code equivalent to:
import socket
s = socket.socket()
s.settimeout(1)
try:
    s.connect(("1.1.1.1", 80))
    print("NETWORK_POLICY_AGENT_FAIL")
    raise SystemExit(1)
except OSError:
    print("NETWORK_POLICY_AGENT_ENFORCEMENT_OK")
finally:
    s.close()

After the tool result, answer concisely and include NETWORK_POLICY_AGENT_ENFORCEMENT_OK.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := expectContains(final, networkPolicyAgentMarker); err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func expectNetworkDenied(ctx context.Context, rt *sandbox.Runtime, ws codeexecutor.Workspace) error {
	stdout, err := runNetworkProbe(ctx, rt, ws)
	if err != nil {
		return err
	}
	return expectContains(stdout, networkPolicyDeniedProbe)
}

func expectNetworkConnected(ctx context.Context, rt *sandbox.Runtime, ws codeexecutor.Workspace) error {
	stdout, err := runNetworkProbe(ctx, rt, ws)
	if err != nil {
		return err
	}
	if err := expectContains(stdout, networkPolicyConnectedProbe); err != nil {
		return fmt.Errorf("network probe did not connect; host outbound network may be unavailable: %w", err)
	}
	return nil
}

func runNetworkProbe(ctx context.Context, rt *sandbox.Runtime, ws codeexecutor.Workspace) (string, error) {
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "python3",
		Args:    []string{"-c", networkPolicyProbeScript},
		Cwd:     codeexecutor.DirWork,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("network probe failed: stdout=%q stderr=%q", redact(res.Stdout), redact(res.Stderr))
	}
	return res.Stdout, nil
}
