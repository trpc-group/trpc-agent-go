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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestLinuxBwrapPreflightArgsMatchRuntimeCore(t *testing.T) {
	withProc := buildBwrapPreflightArgs(true)
	wantWithProc := []string{
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
		"--new-session",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "/bin/true",
	}
	if !reflect.DeepEqual(withProc, wantWithProc) {
		t.Fatalf("buildBwrapPreflightArgs(true) = %#v, want %#v", withProc, wantWithProc)
	}

	withoutProc := buildBwrapPreflightArgs(false)
	wantWithoutProc := []string{
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
		"--new-session",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--perms", "000",
		"--tmpfs", "/proc",
		"--remount-ro", "/proc",
		"--", "/bin/true",
	}
	if !reflect.DeepEqual(withoutProc, wantWithoutProc) {
		t.Fatalf("buildBwrapPreflightArgs(false) = %#v, want %#v", withoutProc, wantWithoutProc)
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
	if !hasArgTriple(withoutProc, "--tmpfs", "/proc", "--remount-ro") {
		t.Fatalf("args = %#v, missing inaccessible /proc mask", withoutProc)
	}
	if !hasArg(withoutProc, "--unshare-pid") {
		t.Fatalf("args = %#v, missing pid isolation", withoutProc)
	}
}

func TestLinuxSandboxArgsWorkspaceMountPolicy(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "workspace-mount-policy", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	spec := codeexecutor.RunProgramSpec{Cmd: "/bin/true"}

	readOnly := ReadOnlyProfile()
	if err := rt.prepareProtectedMasks(readOnly, ws); err != nil {
		t.Fatal(err)
	}
	readOnlyArgs, err := rt.linuxSandboxArgs(readOnly, ws, filepath.Join(ws.Path, "work"), nil, spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(readOnlyArgs, "--ro-bind", "/", "/") {
		t.Fatalf("read-only args = %#v, missing read-only filesystem baseline", readOnlyArgs)
	}
	if hasArgTriple(readOnlyArgs, "--bind", ws.Path, ws.Path) {
		t.Fatalf("read-only args = %#v, workspace was mounted writable", readOnlyArgs)
	}

	readonlyDir := filepath.Join(ws.Path, "work", "readonly")
	if err := os.MkdirAll(readonlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(ws.Path, "work", "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().
		WithReadPaths(readonlyDir).
		WithNoAccessPaths("work/secret.txt")
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(args, "--bind", ws.Path, ws.Path) {
		t.Fatalf("workspace-write args = %#v, missing workspace write mount", args)
	}
	if !hasArgTriple(args, "--ro-bind", readonlyDir, readonlyDir) {
		t.Fatalf("workspace-write args = %#v, missing read-only carveout", args)
	}
	if !hasArgTriple(args, "--ro-bind", filepath.Join(ws.Path, ".git"), filepath.Join(ws.Path, ".git")) {
		t.Fatalf("workspace-write args = %#v, missing protected metadata mask", args)
	}
	if !hasArgTriple(args, "--ro-bind", denyReadMaskSource(ws), secret) {
		t.Fatalf("workspace-write args = %#v, missing no-access file mask", args)
	}
}

func TestLinuxWorkspaceReadOnlyMountArgsSkipWorkspaceAndMissingTargets(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "workspace-ro-mount-skips", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	readonlyDir := filepath.Join(ws.Path, "work", "readonly")
	if err := os.MkdirAll(readonlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missingDir := filepath.Join(ws.Path, "work", "missing")
	profile := WorkspaceWriteProfile().WithReadPaths(ws.Path, readonlyDir, missingDir)

	args, err := rt.workspaceReadOnlyMountArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgTriple(args, "--ro-bind", ws.Path, ws.Path) {
		t.Fatalf("read-only args = %#v, workspace root should be skipped", args)
	}
	if !hasArgTriple(args, "--ro-bind", readonlyDir, readonlyDir) {
		t.Fatalf("read-only args = %#v, missing existing read-only target", args)
	}
	if hasArgTriple(args, "--ro-bind", missingDir, missingDir) {
		t.Fatalf("read-only args = %#v, missing target should be skipped", args)
	}
}

func TestLinuxSandboxArgsRejectRootWriteGrant(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "root-write-grant", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := ReadOnlyProfile()
	profile.fileSystem.Rules = append(profile.fileSystem.Rules, fileSystemRule{
		Kind: ruleSpecial, Access: accessWrite, Special: specialRoot,
	})
	_, err = rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("root write grant error = %v, want ErrPolicyViolation", err)
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
	profile := ReadOnlyProfile().
		WithNoAccessPaths("work/secret.txt").
		WithNoAccessGlobs("work/*.env")
	profile.fileSystem.Rules = append(profile.fileSystem.Rules, fileSystemRule{
		Kind: ruleSpecial, Access: accessNone, Special: specialOut,
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
	if !hasInaccessibleDirMask(args, filepath.Join(ws.Path, codeexecutor.DirOut)) {
		t.Fatalf("mask args = %#v, missing inaccessible mask for out special path", args)
	}
}

func TestLinuxNoAccessMaskArgsSkipMissingReadOnlyPath(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-missing", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := ReadOnlyProfile().WithNoAccessPaths("work/missing.txt")
	args, err := rt.denyReadMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 0 {
		t.Fatalf("missing no-access path args = %#v, want empty", args)
	}
}

func TestLinuxNoAccessMaskArgsMaskMissingPathUnderWritableMount(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-missing-writable", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/missing.txt")
	args, err := rt.denyReadMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(ws.Path, "work", "missing.txt")
	if !hasArgSequence(args, "--perms", "000", "--ro-bind-data", denyReadBindDataFD, missing) {
		t.Fatalf("missing writable no-access path args = %#v, missing placeholder mask", args)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing path placeholder should not be created before bwrap, stat err=%v", err)
	}
}

func TestLinuxNoAccessMaskArgsMaskFirstMissingPathComponent(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-first-missing", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/missing/secret.txt")
	args, err := rt.denyReadMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	firstMissing := filepath.Join(ws.Path, "work", "missing")
	if !hasArgSequence(args, "--perms", "000", "--ro-bind-data", denyReadBindDataFD, firstMissing) {
		t.Fatalf("missing nested no-access path args = %#v, missing first-component placeholder mask", args)
	}
}

func TestLinuxMissingNoAccessPathMaskTargetBranches(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-target-branches", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	writeTarget := filepath.Join(ws.Path, "work")
	externalWriteTarget := t.TempDir()

	target, ok, err := rt.missingNoAccessPathMaskTarget(ws, []string{writeTarget}, "")
	if err != nil || ok || target != "" {
		t.Fatalf("empty path target=%q ok=%v err=%v, want empty false nil", target, ok, err)
	}
	target, ok, err = rt.missingNoAccessPathMaskTarget(ws, []string{writeTarget}, "work")
	if err != nil || ok || target != "" {
		t.Fatalf("existing path target=%q ok=%v err=%v, want empty false nil", target, ok, err)
	}
	target, ok, err = rt.missingNoAccessPathMaskTarget(ws, []string{externalWriteTarget}, "work/missing.txt")
	if err != nil || ok || target != "" {
		t.Fatalf("missing path outside write target=%q ok=%v err=%v, want empty false nil", target, ok, err)
	}
	_, _, err = rt.missingNoAccessPathMaskTarget(ws, []string{writeTarget}, "../escape")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("escaping no-access path error = %v, want ErrPathDenied", err)
	}

	target, ok, err = firstMissingPathComponent("/")
	if err != nil || ok || target != "" {
		t.Fatalf("root first missing target=%q ok=%v err=%v, want empty false nil", target, ok, err)
	}
	_, ok, err = firstMissingPathComponent("relative/missing")
	if !isKind(err, ErrPathDenied) || ok {
		t.Fatalf("relative first missing ok=%v err=%v, want ErrPathDenied", ok, err)
	}
}

func TestCleanupSyntheticDenyReadMaskTargetsRemovesOnlyEmptyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	secondEmpty := filepath.Join(dir, "second-empty")
	nonEmpty := filepath.Join(dir, "non-empty")
	subdir := filepath.Join(dir, "subdir")
	symlink := filepath.Join(dir, "symlink")
	missing := filepath.Join(dir, "missing")

	for _, path := range []string{empty, secondEmpty} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(nonEmpty, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nonEmpty, symlink); err != nil {
		t.Fatal(err)
	}

	cleanupSyntheticDenyReadMaskTargets([]string{
		empty,
		nonEmpty,
		subdir,
		symlink,
		missing,
		secondEmpty,
	})

	for _, path := range []string{empty, secondEmpty} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("empty regular file %s still exists, err=%v", path, err)
		}
	}
	for _, path := range []string{nonEmpty, subdir, symlink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("cleanup removed %s: %v", path, err)
		}
	}
}

func TestLinuxValidateNoAccessMaskBranches(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-validation-branches", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	noWrites := ReadOnlyProfile().WithNoAccessGlobs("/absolute/*.secret")
	if err := rt.validateNoAccessMasksEnforceable(noWrites, ws); err != nil {
		t.Fatalf("no write mounts should skip glob enforceability checks: %v", err)
	}

	invalidRule := ReadOnlyProfile()
	invalidRule.fileSystem.Rules = append(invalidRule.fileSystem.Rules, fileSystemRule{
		Kind: ruleGlob, Access: accessRead, Glob: "work/*.secret",
	})
	if err := rt.validateNoAccessMasksEnforceable(invalidRule, ws); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("invalid rule error = %v, want ErrPolicyViolation", err)
	}

	blankGlob := ReadOnlyProfile().WithWritePaths("work")
	blankGlob.fileSystem.Rules = append(blankGlob.fileSystem.Rules, fileSystemRule{
		Kind: ruleGlob, Access: accessNone, Glob: " ",
	})
	if err := rt.validateNoAccessMasksEnforceable(blankGlob, ws); err != nil {
		t.Fatalf("blank no-access glob should be ignored: %v", err)
	}

	absoluteGlob := ReadOnlyProfile().WithWritePaths("work").WithNoAccessGlobs("/tmp/*.secret")
	err = rt.validateNoAccessMasksEnforceable(absoluteGlob, ws)
	if !isKind(err, ErrPolicyViolation) ||
		!strings.Contains(err.Error(), "linux backend requires workspace-relative glob denials") {
		t.Fatalf("absolute no-access glob error = %v, want ErrPolicyViolation", err)
	}
}

func TestLinuxNoAccessMaskArgsRejectGlobUnderWritableMount(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-glob-writable", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := ReadOnlyProfile().
		WithWritePaths("work").
		WithNoAccessGlobs("work/*.env")
	_, err = rt.denyReadMaskArgs(profile, ws)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("writable no-access glob error = %v, want ErrPolicyViolation", err)
	}
	if !strings.Contains(err.Error(), "no-access-glob work/*.env") ||
		!strings.Contains(err.Error(), "glob denial overlaps writable mount work") {
		t.Fatalf("writable no-access glob error = %v, want glob and writable mount context", err)
	}
}

func TestLinuxDenyReadMaskSetupPropagatesGlobError(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-mask-invalid-glob", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := ReadOnlyProfile().WithNoAccessGlobs("[")
	_, err = rt.denyReadMaskSetup(profile, ws)
	if err == nil {
		t.Fatalf("denyReadMaskSetup unexpectedly accepted invalid glob")
	}
}

func TestLinuxBackendCapabilitiesAndSandboxArgsBranches(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "linux-args-branches", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	externalRead := t.TempDir()
	externalWrite := t.TempDir()
	profile := WorkspaceWriteProfile().
		WithReadPaths(externalRead).
		WithWritePaths(externalWrite, filepath.Join(ws.Path, "work")).
		WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		[]string{"GOOD=1", "MALFORMED", "=empty"},
		codeexecutor.RunProgramSpec{Cmd: "/bin/echo", Args: []string{"ok"}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasArg(args, "--unshare-net") {
		t.Fatalf("args = %#v, unexpected network isolation for enabled network", args)
	}
	if !hasArgTriple(args, "--ro-bind", externalRead, externalRead) {
		t.Fatalf("args = %#v, missing external read grant", args)
	}
	if !hasArgTriple(args, "--bind", externalWrite, externalWrite) {
		t.Fatalf("args = %#v, missing external write grant", args)
	}
	if !hasArgPair(args, "--setenv", "GOOD") || !hasArg(args, "1") {
		t.Fatalf("args = %#v, missing GOOD env", args)
	}
	if hasArg(args, "MALFORMED") || hasArg(args, "=empty") {
		t.Fatalf("args = %#v, malformed env was not skipped", args)
	}

	caps := backendCapabilities(BackendLinuxBubblewrap, profile)
	if !caps.OSSandbox || !caps.NetworkIsolation || !caps.ExternalPathGrants {
		t.Fatalf("managed capabilities = %#v, want sandbox features", caps)
	}
	disabledCaps := backendCapabilities(BackendAuto, DangerFullAccessProfile())
	if disabledCaps.OSSandbox || disabledCaps.NetworkIsolation || disabledCaps.ProtectedPathMasks {
		t.Fatalf("disabled capabilities = %#v, want no managed sandbox features", disabledCaps)
	}
}

func TestLinuxProtectedMaskArgsSkipBlankDotAndMissing(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "protected-mask-skip", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ws.Path, "present"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile()
	profile.fileSystem.ProtectedMetadata = []string{"", ".", "missing", "present"}
	args, err := rt.protectedMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	present := filepath.Join(ws.Path, "present")
	if !reflect.DeepEqual(args, []string{"--ro-bind", present, present}) {
		t.Fatalf("protected args = %#v, want present ro-bind only", args)
	}
}

func TestLinuxPrepareProtectedMasksRejectsEscapes(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "protected-mask-escape", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile()
	profile.fileSystem.ProtectedMetadata = []string{"../escape"}
	err = rt.prepareProtectedMasks(profile, ws)
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("prepareProtectedMasks error = %v, want ErrPathDenied", err)
	}
}

func TestLinuxPrepareProtectedMasksSkipsBlankDotAndCreatesMissing(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "protected-mask-create", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile()
	profile.fileSystem.ProtectedMetadata = []string{"", ".", "work/missing-meta"}
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "work", "missing-meta")); err != nil {
		t.Fatalf("missing protected metadata was not created: %v", err)
	}
	if _, err := os.Stat(denyReadMaskSource(ws)); err != nil {
		t.Fatalf("deny-read mask source was not created: %v", err)
	}
}

func TestLinuxPreflightUnsupportedBackendAndProbeError(t *testing.T) {
	rt := NewRuntime(WithBackend(BackendType("not-linux-bubblewrap")))
	_, _, err := rt.linuxPreflight()
	if !isKind(err, ErrUnsupportedBackend) {
		t.Fatalf("linuxPreflight error = %v, want ErrUnsupportedBackend", err)
	}
	ws := codeexecutor.Workspace{ID: "unsupported", Path: t.TempDir()}
	_, backend, _, err := rt.osSandboxCommand(
		context.Background(),
		WorkspaceWriteProfile(),
		ws,
		ws.Path,
		nil,
		codeexecutor.RunProgramSpec{Cmd: "true"},
		sandboxDenialRun{},
	)
	if backend != string(BackendLinuxBubblewrap) || !isKind(err, ErrUnsupportedBackend) {
		t.Fatalf("osSandboxCommand backend=%q err=%v, want bubblewrap unsupported", backend, err)
	}

	argRuntime := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	argWS, err := argRuntime.CreateWorkspace(context.Background(), "linux/error-args", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	missingGrant := WorkspaceWriteProfile().WithReadPaths(filepath.Join(t.TempDir(), "missing"))
	_, err = argRuntime.linuxSandboxArgs(
		missingGrant,
		argWS,
		filepath.Join(argWS.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "true"},
		false,
	)
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("missing external grant error = %v, want ErrPathDenied", err)
	}

	probeErr := bwrapProbeError{
		err:    errors.New("probe failed"),
		stderr: "stderr detail",
		hint:   "try installing bubblewrap",
	}
	if got := probeErr.Error(); !strings.Contains(got, "probe failed: stderr detail; try installing bubblewrap") {
		t.Fatalf("probe error string = %q", got)
	}
	if !errors.Is(probeErr, probeErr.err) {
		t.Fatalf("probe error did not unwrap cause")
	}
	if !containsAny("permission denied", []string{"missing", "denied"}) {
		t.Fatalf("containsAny did not match substring")
	}
}

func TestLinuxPreflightMissingBwrap(t *testing.T) {
	t.Setenv("PATH", "")
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	_, _, err := rt.linuxPreflight()
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("linuxPreflight error = %v, want ErrSetupFailed", err)
	}
}

func TestLinuxPreflightFallsBackWhenProcMountFails(t *testing.T) {
	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte(`#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--proc" ]; then
		echo "bwrap: Can't mount proc on /newroot/proc: Operation not permitted" >&2
		exit 1
	fi
done
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	path, mountProc, err := rt.linuxPreflight()
	if err != nil {
		t.Fatalf("linuxPreflight error = %v, want fallback success", err)
	}
	if path != bwrap || mountProc {
		t.Fatalf("linuxPreflight path=%q mountProc=%v, want %q false", path, mountProc, bwrap)
	}
}

func TestLinuxPreflightReturnsProbeFailure(t *testing.T) {
	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte(`#!/bin/sh
echo "bwrap: generic failure" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	_, _, err := rt.linuxPreflight()
	if !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "generic failure") {
		t.Fatalf("linuxPreflight error = %v, want probe setup failure", err)
	}
}

func TestLinuxSandboxCommandBindsDenyReadDataFDAndCleansSyntheticTargets(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	rt.preflightOnce.Do(func() {
		rt.bwrapPath = "/bin/true"
	})
	ws, err := rt.CreateWorkspace(context.Background(), "linux/sandbox-command-cleanup", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(ws.Path, "work", "missing.txt")
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/missing.txt")

	cmd, backend, cleanup, err := rt.osSandboxCommand(
		context.Background(),
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		[]string{"SANDBOX_TEST=1"},
		codeexecutor.RunProgramSpec{Cmd: "/bin/echo", Args: []string{"ok"}},
		sandboxDenialRun{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if backend != string(BackendLinuxBubblewrap) {
		t.Fatalf("backend = %q, want %q", backend, BackendLinuxBubblewrap)
	}
	if cmd.Path != "/bin/true" {
		t.Fatalf("cmd path = %q, want fake bwrap", cmd.Path)
	}
	if len(cmd.ExtraFiles) != 1 {
		t.Fatalf("extra files = %d, want deny-read data fd", len(cmd.ExtraFiles))
	}
	if cleanup == nil {
		t.Fatalf("cleanup is nil, want deny-read data fd cleanup")
	}
	if !hasArgSequence(cmd.Args, "--perms", "000", "--ro-bind-data", denyReadBindDataFD, missing) {
		t.Fatalf("cmd args = %#v, missing bind-data mask for synthetic target", cmd.Args)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("synthetic target should not exist before sandbox start, stat err=%v", err)
	}

	if err := os.WriteFile(missing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove synthetic target, stat err=%v", err)
	}
	if _, err := cmd.ExtraFiles[0].Stat(); err == nil {
		t.Fatalf("cleanup did not close deny-read data fd")
	}
}

func TestLinuxSandboxCommandPropagatesSetupError(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	rt.preflightOnce.Do(func() {
		rt.bwrapPath = "/bin/true"
	})
	ws, err := rt.CreateWorkspace(context.Background(), "linux/sandbox-command-setup-error", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithReadPaths("work/\x00blocked")
	_, backend, cleanup, err := rt.osSandboxCommand(
		context.Background(),
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "true"},
		sandboxDenialRun{},
	)
	if backend != string(BackendLinuxBubblewrap) || err == nil || cleanup != nil {
		t.Fatalf("osSandboxCommand backend=%q cleanup=%v err=%v, want setup error", backend, cleanup, err)
	}
}

func TestLinuxSandboxCommandPrepareAndArgsErrors(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	rt.preflightOnce.Do(func() {
		rt.bwrapPath = "/bin/true"
	})

	wsPath := filepath.Join(t.TempDir(), "workspace-file")
	if err := os.WriteFile(wsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := codeexecutor.Workspace{ID: "bad", Path: wsPath}
	_, backend, _, err := rt.osSandboxCommand(
		context.Background(),
		WorkspaceWriteProfile(),
		ws,
		ws.Path,
		nil,
		codeexecutor.RunProgramSpec{Cmd: "true"},
		sandboxDenialRun{},
	)
	if backend != string(BackendLinuxBubblewrap) || err == nil {
		t.Fatalf("osSandboxCommand backend=%q err=%v, want prepare error", backend, err)
	}

	argRuntime := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	argWS, err := argRuntime.CreateWorkspace(context.Background(), "linux/mount-errors", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile()
	profile.fileSystem.ProtectedMetadata = []string{"work/\x00blocked"}
	_, err = argRuntime.linuxSandboxArgs(
		profile,
		argWS,
		filepath.Join(argWS.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "true"},
		false,
	)
	if err == nil {
		t.Fatalf("linuxSandboxArgs unexpectedly succeeded for unreadable protected path")
	}
}

func TestLinuxWorkspaceMountArgsErrorAndDedupBranches(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "linux/mount-args-branches", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.workspaceWriteMountArgs(ReadOnlyProfile().WithWritePaths("work/missing"), ws)
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("missing write mount target error = %v, want ErrPathDenied", err)
	}

	externalWrite := t.TempDir()
	profile := ReadOnlyProfile().WithWritePaths(externalWrite, externalWrite, filepath.Join(ws.Path, "work"))
	targets, err := rt.linuxWriteMountTargets(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	var externalCount int
	for _, target := range targets {
		if target == externalWrite {
			externalCount++
		}
	}
	if externalCount != 1 {
		t.Fatalf("linuxWriteMountTargets targets=%#v, want one external write target", targets)
	}
}

func TestLinuxWorkspaceMountTargetBranches(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "linux/mount-targets", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		t.Fatal(err)
	}

	target, ok, err := rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: rulePath, Access: accessRead,
	})
	if err != nil || ok || target != "" {
		t.Fatalf("empty path target=%q ok=%v err=%v, want empty false nil", target, ok, err)
	}

	inside := filepath.Join(ws.Path, "work")
	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: inside,
	})
	if err != nil || !ok || target != inside {
		t.Fatalf("absolute inside target=%q ok=%v err=%v", target, ok, err)
	}

	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: t.TempDir(),
	})
	if err != nil || ok || target != "" {
		t.Fatalf("external absolute target=%q ok=%v err=%v, want skipped", target, ok, err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws.Path, "work", "escape")); err != nil {
		t.Fatal(err)
	}
	_, _, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: filepath.Join(ws.Path, "work", "escape"),
	})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("symlink escape target error = %v, want ErrPathDenied", err)
	}

	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: "work",
	})
	if err != nil || !ok || target != inside {
		t.Fatalf("relative target=%q ok=%v err=%v", target, ok, err)
	}

	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: ruleSpecial, Access: accessRead, Special: specialRoot,
	})
	if err != nil || ok || target != "" {
		t.Fatalf("read root target=%q ok=%v err=%v, want skipped", target, ok, err)
	}

	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: ruleSpecial, Access: accessRead, Special: specialWork,
	})
	if err != nil || !ok || target != filepath.Join(ws.Path, codeexecutor.DirWork) {
		t.Fatalf("special work target=%q ok=%v err=%v", target, ok, err)
	}

	target, ok, err = rt.workspaceMountTarget(ws, wsAbs, fileSystemRule{
		Kind: fileSystemRuleKind("unknown"), Access: accessRead,
	})
	if err != nil || ok || target != "" {
		t.Fatalf("unknown target=%q ok=%v err=%v, want skipped", target, ok, err)
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

func hasArgSequence(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		ok := true
		for j, arg := range want {
			if args[i+j] != arg {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func hasInaccessibleDirMask(args []string, target string) bool {
	for i := 0; i+5 < len(args); i++ {
		if args[i] == "--perms" &&
			args[i+1] == "000" &&
			args[i+2] == "--tmpfs" &&
			args[i+3] == target &&
			args[i+4] == "--remount-ro" &&
			args[i+5] == target {
			return true
		}
	}
	return false
}
