//go:build linux

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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestLinuxBwrapWorkspaceWriteIntegration(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, _, err := rt.linuxPreflight(); err != nil {
		t.Skipf("bubblewrap preflight unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "echo ok > ok.txt; echo bad > ../.git/config"},
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("protected metadata write unexpectedly succeeded: %#v", res)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, "work", "ok.txt"))
	if err != nil {
		t.Fatalf("workspace write missing: %v result=%#v", err, res)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("workspace write failed: %q", data)
	}
}

func TestLinuxProcMountFailureDetection(t *testing.T) {
	for _, stderr := range []string{
		"bwrap: Can't mount proc on /newroot/proc: Invalid argument",
		"bwrap: Can't mount proc on /newroot/proc: Operation not permitted",
		"bwrap: Can't mount proc on /newroot/proc: Permission denied",
	} {
		if !isProcMountFailure(stderr) {
			t.Fatalf("isProcMountFailure(%q) = false, want true", stderr)
		}
	}

	for _, stderr := range []string{
		"bwrap: Can't bind mount /dev/null: Operation not permitted",
		"bwrap: Can't access /newroot/proc/sysrq-trigger: Read-only file system",
		"bwrap: Can't access /newroot/proc/sysrq-trigger: Permission denied",
		"bwrap: Can't mount proc on /newroot/proc: No such file or directory",
	} {
		if isProcMountFailure(stderr) {
			t.Fatalf("isProcMountFailure(%q) = true, want false", stderr)
		}
	}
}

func TestLinuxSandboxArgsToggleProcMount(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "proc-toggle", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile()
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	spec := codeexecutor.RunProgramSpec{Cmd: "/bin/true"}

	withProc, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(withProc, "--proc", "/proc") {
		t.Fatalf("args = %#v, missing --proc /proc", withProc)
	}

	withoutProc, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgPair(withoutProc, "--proc", "/proc") {
		t.Fatalf("args = %#v, unexpected --proc /proc", withoutProc)
	}
	if !hasArg(withoutProc, "--unshare-pid") {
		t.Fatalf("args = %#v, missing pid isolation", withoutProc)
	}
}

func TestLinuxNoAccessMaskArgsCoverPathGlobAndSpecial(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-args", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "app.env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().
		WithNoAccessPaths("work/secret.txt").
		WithNoAccessGlobs("work/*.env")
	profile.FileSystem.Rules = append(profile.FileSystem.Rules, FileSystemRule{
		Kind: RuleSpecial, Access: AccessNone, Special: SpecialOut,
	})
	args, err := rt.denyReadMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	mask := denyReadMaskSource(ws)
	for _, want := range []string{
		filepath.Join(ws.Path, "work", "secret.txt"),
		filepath.Join(ws.Path, "work", "app.env"),
	} {
		if !hasArgTriple(args, "--ro-bind", mask, want) {
			t.Fatalf("mask args = %#v, missing ro-bind mask for %s", args, want)
		}
	}
	if !hasArgPair(args, "--tmpfs", filepath.Join(ws.Path, codeexecutor.DirOut)) {
		t.Fatalf("mask args = %#v, missing tmpfs for out special path", args)
	}
}

func hasArgPair(args []string, first, second string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasArgTriple(args []string, first, second, third string) bool {
	for i := 0; i+2 < len(args); i++ {
		if args[i] == first && args[i+1] == second && args[i+2] == third {
			return true
		}
	}
	return false
}
