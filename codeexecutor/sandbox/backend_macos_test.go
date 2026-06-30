//go:build darwin

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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestMacOSBackendCapabilities(t *testing.T) {
	caps := backendCapabilities(BackendMacOSSandboxExec, WorkspaceWriteProfile())
	if !caps.OSSandbox || !caps.NetworkIsolation || !caps.DenyReadGlob ||
		!caps.ExternalPathGrants || !caps.ProtectedPathMasks {
		t.Fatalf("managed capabilities = %#v, want macOS sandbox features", caps)
	}
	unsupportedCaps := backendCapabilities(BackendLinuxBubblewrap, WorkspaceWriteProfile())
	if unsupportedCaps.OSSandbox || unsupportedCaps.NetworkIsolation ||
		unsupportedCaps.DenyReadGlob || unsupportedCaps.ExternalPathGrants {
		t.Fatalf("unsupported backend capabilities = %#v, want no macOS sandbox features", unsupportedCaps)
	}
	disabledCaps := backendCapabilities(BackendAuto, DangerFullAccessProfile())
	if disabledCaps.OSSandbox || disabledCaps.NetworkIsolation || disabledCaps.ProtectedPathMasks {
		t.Fatalf("disabled capabilities = %#v, want no managed sandbox features", disabledCaps)
	}
}

func TestMacOSSeatbeltProfileGeneration(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "macos/profile", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	externalRead := t.TempDir()
	externalWrite := t.TempDir()
	profile := WorkspaceWriteProfile().
		WithReadPaths(externalRead, "work/read-only").
		WithWritePaths(externalWrite).
		WithNoAccessPaths("work/secret").
		WithNoAccessGlobs("work/*.env").
		WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	policy, err := rt.macosSeatbeltProfile(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	externalReadPolicyPath, err := canonicalizeExistingPath(externalRead)
	if err != nil {
		t.Fatal(err)
	}
	externalWritePolicyPath, err := canonicalizeExistingPath(externalWrite)
	if err != nil {
		t.Fatal(err)
	}
	secretPolicyPath, err := canonicalizeExistingPath(filepath.Join(ws.Path, "work", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(deny default)",
		"(allow file-read* file-map-executable file-test-existence",
		"(allow file-write*",
		sbplString(externalReadPolicyPath),
		sbplString(externalWritePolicyPath),
		"(require-not (literal " + sbplString(secretPolicyPath) + "))",
		`(deny file-read* file-map-executable file-test-existence (regex #"`,
		`(deny file-write* (regex #"`,
		"(allow network-outbound)",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("macOS policy missing %q:\n%s", want, policy)
		}
	}
}

func TestMacOSGlobRegexTranslation(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := codeexecutor.Workspace{Path: wsPath}
	regex, ok, err := macosSeatbeltRegexForWorkspaceGlob(ws, "**/*.env")
	if err != nil || !ok {
		t.Fatalf("glob regex err=%v ok=%v", err, ok)
	}
	if !strings.HasPrefix(regex, "^") || !strings.HasSuffix(regex, "$") ||
		!strings.Contains(regex, "(.*/)?") || !strings.Contains(regex, `\.env`) {
		t.Fatalf("glob regex = %q, want anchored doublestar .env regex", regex)
	}
	_, _, err = macosSeatbeltRegexForWorkspaceGlob(ws, "/tmp/*.env")
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("absolute glob error = %v, want ErrPolicyViolation", err)
	}
	_, _, err = macosSeatbeltRegexForWorkspaceGlob(ws, "work/[")
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("invalid glob error = %v, want ErrPolicyViolation", err)
	}
}

func TestMacOSSandboxExecWorkspaceWriteIntegration(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/run", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh",
		Args: []string{
			"-c",
			"echo ok > ok.txt; mkdir ../.git 2>&1; echo bad > ../.git/config 2>/dev/null",
		},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("protected metadata write unexpectedly succeeded: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, ".git")); !os.IsNotExist(err) {
		t.Fatalf("protected metadata dir should remain absent: err=%v result=%#v", err, res)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, "work", "ok.txt"))
	if err != nil {
		t.Fatalf("workspace write missing: %v result=%#v", err, res)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("workspace write failed: %q", data)
	}
}

func TestMacOSSandboxExecNoAccessGlobIntegration(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/glob", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "app.env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "cat app.env"},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("glob no-access read unexpectedly succeeded: %#v", res)
	}
}

func TestMacOSSandboxExecNoAccessGlobHardDenyOverridesSpecificRead(t *testing.T) {
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		t.Skip("sandbox-exec not available")
	}
	profile := ReadOnlyProfile().
		WithReadPaths("work/public/secret.txt").
		WithNoAccessGlobs("work/**")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	if _, err := rt.macosPreflight(); err != nil {
		t.Skipf("sandbox-exec preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "macos/glob-hard-deny", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(ws.Path, "work", "public", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(secret), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("visible to Go layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"work/public/secret.txt"})
	if err != nil || len(files) != 1 {
		t.Fatalf("Go-layer Collect = files:%d err:%v, want specific read grant to win", len(files), err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/bin/sh",
		Args: []string{"-c", "cat work/public/secret.txt"},
		Cwd:  ".",
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("glob hard deny was reopened by specific read grant: %#v", res)
	}
}
