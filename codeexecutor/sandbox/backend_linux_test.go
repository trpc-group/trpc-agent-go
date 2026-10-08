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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestLinuxBwrapWorkspaceWriteIntegration(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap not available")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, _, err := rt.linuxPreflight(context.Background()); err != nil {
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
		"--cap-drop", "ALL",
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
		"--cap-drop", "ALL",
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
	if !hasArgPair(withProc, "--seccomp", "3") || !hasArgPair(withoutProc, "--seccomp", "3") {
		t.Fatalf("restricted args missing --seccomp 3: with=%#v without=%#v", withProc, withoutProc)
	}
	if !hasArg(withProc, "--unshare-net") || !hasArg(withoutProc, "--unshare-net") {
		t.Fatalf("restricted args missing --unshare-net")
	}
	if !hasArgPair(withProc, "--cap-drop", "ALL") ||
		!hasArgPair(withoutProc, "--cap-drop", "ALL") {
		t.Fatalf("restricted args missing --cap-drop ALL")
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
	if !hasArgTriple(readOnlyArgs, "--ro-bind", ws.Path, ws.Path) || hasArgTriple(readOnlyArgs, "--ro-bind", "/", "/") {
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
	sessionsRoot, err := filepath.Abs(filepath.Join(rt.root, "sandbox"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(args, "--tmpfs", sessionsRoot) {
		t.Fatalf("workspace-write args = %#v, missing sibling session hide", args)
	}
}

func TestLinuxDeniedParentChildGrantRejected(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprint(external), func(t *testing.T) {
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
			ws, err := rt.CreateWorkspace(context.Background(), "current", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(ws.Path, "work", "denied")
			if external {
				parent = t.TempDir()
			}
			child := filepath.Join(parent, "allowed")
			if err := os.MkdirAll(child, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, write := range []bool{false, true} {
				profile := WorkspaceWriteProfile().WithNoAccessPaths(parent).WithReadPaths(child)
				if write {
					profile = profile.WithWritePaths(child)
				}
				if err := rt.prepareProtectedMasks(profile, ws); err != nil {
					t.Fatal(err)
				}
				_, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}, false)
				if !isKind(err, ErrPolicyViolation) {
					t.Fatalf("silently accepted a grant hidden by a parent mask (write=%v): %v", write, err)
				}
			}
		})
	}
}

func TestLinuxSandboxArgsGrantedOmitsHostRoot(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "granted-root", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(WorkspaceWriteProfile().WithReadMode(ReadModeGranted), ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		WorkspaceWriteProfile().WithReadMode(ReadModeGranted),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgTriple(args, "--ro-bind", "/", "/") {
		t.Fatalf("granted args = %#v, unexpected host root bind", args)
	}
	if !hasArgTriple(args, "--ro-bind-try", "/usr", "/usr") {
		t.Fatalf("granted args = %#v, missing runtime /usr bind", args)
	}
	if !hasArgPair(args, "--tmpfs", "/tmp") {
		t.Fatalf("granted args = %#v, missing private /tmp", args)
	}
	if !hasArgTriple(args, "--bind", ws.Path, ws.Path) {
		t.Fatalf("granted args = %#v, missing workspace write bind", args)
	}
}

func TestLinuxHostReadRuleUnderWritableAncestor(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		t.Run(string(mode), func(t *testing.T) {
			parent := t.TempDir()
			readOnly := filepath.Join(parent, "readonly")
			if err := os.WriteFile(readOnly, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Optional runtime rules must retain their effective access when
			// an explicit ancestor write grant replaces the host baseline.
			profile := WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(parent)
			profile = profile.withFileSystemRule(fileSystemRule{
				Kind: rulePath, Access: accessRead, Path: readOnly, optional: true,
			})
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
			ws, err := rt.CreateWorkspace(ctx, "runtime-carveout", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `cat "$1" || exit 1
if (printf forbidden > "$1") 2>/dev/null; then exit 2; fi
printf changed > "$2" && cat "$2"`, "test", readOnly, filepath.Join(parent, "writable")},
			})
			if err != nil || result.ExitCode != 0 || result.Stdout != "originalchanged" {
				t.Fatalf("optional read grant beneath writable ancestor: %#v, %v", result, err)
			}
			data, err := os.ReadFile(readOnly)
			if err != nil || string(data) != "original" {
				t.Fatalf("read-only runtime resource changed: %q, %v", data, err)
			}
		})
	}
}

func TestLinuxSandboxArgsWorkspaceRootAliasProtections(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/secret")
	rt := NewRuntime(WithWorkspaceRoot(alias))
	ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(ws.Path, "work", "secret")
	if err := os.WriteFile(secret, []byte("denied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}, false)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(ws.Path, ".git")
	if !hasArgTriple(args, "--ro-bind", metadata, metadata) {
		t.Fatalf("missing metadata protection at workspace bind destination: %#v", args)
	}
	if !hasArgTriple(args, "--ro-bind", denyReadMaskSource(ws), secret) {
		t.Fatalf("missing no-access mask at workspace bind destination: %#v", args)
	}
}

func TestLinuxBwrapWorkspaceRootAliasProtections(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, aliasRoot := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/alias=%v", mode, aliasRoot), func(t *testing.T) {
				root := t.TempDir()
				if aliasRoot {
					alias := filepath.Join(t.TempDir(), "workspace-root")
					if err := os.Symlink(root, alias); err != nil {
						t.Fatal(err)
					}
					root = alias
				}
				profile := WorkspaceWriteProfile().WithReadMode(mode).
					WithNoAccessPaths("work/secret", "work/missing", "work/denied")
				rt := NewRuntime(WithWorkspaceRoot(root), WithPermissionProfile(profile))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				for path, content := range map[string]string{
					"work/secret": "denied", "work/denied/data": "denied",
					"work/public": "public", ".git/config": "original",
				} {
					path = filepath.Join(ws.Path, path)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				script := `cat "$1/work/public" || exit 1
if cat "$1/work/secret" 2>/dev/null; then exit 2; fi
if cat "$1/work/denied/data" 2>/dev/null; then exit 3; fi
if (printf forbidden > "$1/.git/config") 2>/dev/null; then exit 4; fi
if (printf forbidden > "$1/work/missing") 2>/dev/null; then exit 5; fi
printf changed > "$1/work/writable" && cat "$1/work/writable"`
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", script, "test", ws.Path},
				})
				if err != nil || result.ExitCode != 0 || result.Stdout != "publicchanged" {
					t.Fatalf("workspace alias protections: %#v, %v", result, err)
				}
				data, err := os.ReadFile(filepath.Join(ws.Path, ".git", "config"))
				if err != nil || string(data) != "original" {
					t.Fatalf("protected metadata changed: %q, %v", data, err)
				}
				if _, err := os.Stat(filepath.Join(ws.Path, "work", "missing")); !os.IsNotExist(err) {
					t.Fatalf("missing no-access target was created: %v", err)
				}
			})
		}
	}
}

func TestLinuxBwrapExternalAliasCarveouts(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, layout := range []string{"direct", "alias-child", "canonical-child", "both-parents"} {
			t.Run(string(mode)+"/"+layout, func(t *testing.T) {
				parent := t.TempDir()
				readOnly := filepath.Join(parent, "readonly")
				writable := filepath.Join(readOnly, "writable")
				if err := os.MkdirAll(writable, 0o755); err != nil {
					t.Fatal(err)
				}
				dataPath := filepath.Join(readOnly, "data")
				if err := os.WriteFile(dataPath, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
				visibleParent := parent
				if layout != "direct" {
					visibleParent = filepath.Join(t.TempDir(), "host-parent")
					if err := os.Symlink(parent, visibleParent); err != nil {
						t.Fatal(err)
					}
				}
				childRule := filepath.Join(visibleParent, "readonly")
				if layout == "canonical-child" {
					childRule = readOnly
				}
				// Add the child write grant before its read-only parent so rule
				// order cannot accidentally replace the more-specific carveout.
				profile := WorkspaceWriteProfile().WithReadMode(mode).
					WithWritePaths(filepath.Join(childRule, "writable"), visibleParent).
					WithReadPaths(childRule)
				if layout == "both-parents" {
					profile = profile.WithWritePaths(parent)
				}
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				script := `cat "$1/readonly/data" || exit 1
if (printf forbidden > "$1/readonly/data") 2>/dev/null; then exit 2; fi
if test -r "$2" && (printf forbidden > "$2") 2>/dev/null; then exit 5; fi
printf changed > "$1/readonly/writable/data" || exit 3
printf writable > "$1/public" || exit 4
cat "$1/readonly/writable/data" "$1/public"`
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", script, "test", visibleParent, dataPath},
				})
				if err != nil || result.ExitCode != 0 || result.Stdout != "originalchangedwritable" {
					t.Fatalf("external alias carveouts: %#v, %v", result, err)
				}
				data, err := os.ReadFile(dataPath)
				if err != nil || string(data) != "original" {
					t.Fatalf("read-only resource changed: %q, %v", data, err)
				}
			})
		}
	}
}

func TestLinuxBwrapGrantedAncestorChildSymlink(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, absoluteLink := range []bool{false, true} {
		for _, aliasParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("absolute=%v/alias=%v", absoluteLink, aliasParent), func(t *testing.T) {
				parent := t.TempDir()
				readOnly := filepath.Join(parent, "readonly")
				linkTarget := "readonly"
				if absoluteLink {
					readOnly = t.TempDir()
					linkTarget = readOnly
				}
				if err := os.MkdirAll(readOnly, 0o755); err != nil {
					t.Fatal(err)
				}
				dataPath := filepath.Join(readOnly, "data")
				if err := os.WriteFile(dataPath, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(linkTarget, filepath.Join(parent, "link")); err != nil {
					t.Fatal(err)
				}
				visibleParent := parent
				if aliasParent {
					visibleParent = filepath.Join(t.TempDir(), "host-parent")
					if err := os.Symlink(parent, visibleParent); err != nil {
						t.Fatal(err)
					}
				}
				writable := filepath.Join(visibleParent, "writable")
				if err := os.MkdirAll(writable, 0o755); err != nil {
					t.Fatal(err)
				}
				profile := WorkspaceWriteProfile().WithReadPaths(visibleParent).
					WithWritePaths(writable).
					WithReadPaths(filepath.Join(visibleParent, "link"))
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", `cat "$1/link/data" || exit 1
if (printf forbidden > "$1/link/data") 2>/dev/null; then exit 2; fi
printf changed > "$1/writable/data" && cat "$1/writable/data"`, "test", visibleParent},
				})
				if err != nil || result.ExitCode != 0 || result.Stdout != "originalchanged" {
					t.Fatalf("child symlink under a bound ancestor: %#v, %v", result, err)
				}
				data, err := os.ReadFile(dataPath)
				if err != nil || string(data) != "original" {
					t.Fatalf("read-only link target changed: %q, %v", data, err)
				}
			})
		}
	}
}

func TestLinuxBwrapProtectedSymlinkEntries(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, layout := range []string{"metadata", "credential", "credential-ancestor", "no-access", "no-access-ancestor", "read-only", "read-only-ancestor"} {
			for _, writableParent := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/write=%v", mode, layout, writableParent), func(t *testing.T) {
					profile := ReadOnlyProfile().WithReadMode(mode)
					if writableParent {
						profile = WorkspaceWriteProfile().WithReadMode(mode)
					}
					rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					target := filepath.Join(ws.Path, "work", "source")
					entry := filepath.Join(ws.Path, ".git")
					switch layout {
					case "credential", "credential-ancestor":
						home := t.TempDir()
						t.Setenv("HOME", home)
						target = t.TempDir()
						entry = filepath.Join(home, ".ssh")
						if layout == "credential-ancestor" {
							entry = filepath.Join(home, ".config")
						}
						profile = profile.WithReadPaths(home)
						if writableParent {
							profile = profile.WithWritePaths(home)
						}
					case "no-access", "no-access-ancestor", "read-only", "read-only-ancestor":
						entry = filepath.Join(ws.Path, "work", "alias")
						denied := entry
						if strings.HasSuffix(layout, "-ancestor") {
							denied = filepath.Join(entry, "config")
						}
						if strings.HasPrefix(layout, "read-only") {
							profile = profile.WithReadPaths(denied)
						} else {
							profile = profile.WithNoAccessPaths(denied)
						}
					}
					dataPath := filepath.Join(target, "config")
					if layout == "credential-ancestor" {
						dataPath = filepath.Join(target, "gcloud", "credentials.db")
					}
					if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(dataPath, []byte("original"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, entry); err != nil {
						t.Fatal(err)
					}
					rt.profile = profile
					result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "/bin/rm", Args: []string{entry}})
					if writableParent {
						if !isKind(err, ErrPolicyViolation) {
							t.Fatalf("mutable protected entry must fail closed: %#v, %v", result, err)
						}
					} else if err != nil || result.ExitCode == 0 {
						t.Fatalf("read-only parent must protect the link entry: %#v, %v", result, err)
					}
					if info, err := os.Lstat(entry); err != nil || info.Mode()&os.ModeSymlink == 0 {
						t.Fatalf("protected link entry changed: %v", err)
					}
					data, err := os.ReadFile(dataPath)
					if err != nil || string(data) != "original" {
						t.Fatalf("protected contents changed: %q, %v", data, err)
					}
				})
			}
		}
	}
}

func TestLinuxBwrapExplicitCredentialSymlinkWriteGrant(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	entry := filepath.Join(home, ".ssh")
	target := t.TempDir()
	if err := os.Symlink(target, entry); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithWritePaths(home, entry)
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
	ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh", Args: []string{"-c", `printf allowed > "$1/config" && cat "$1/config"`, "test", entry},
	})
	if err != nil || result.ExitCode != 0 || result.Stdout != "allowed" {
		t.Fatalf("exact credential write grant rejected: %#v, %v", result, err)
	}
}

func TestLinuxNoAccessDanglingSymlinkFailsClosed(t *testing.T) {
	parent := t.TempDir()
	entry := filepath.Join(parent, "denied")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), entry); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithWritePaths(parent).WithNoAccessPaths(entry)
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "current", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	_, err = rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}, false)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("dangling no-access link beneath a writable parent was accepted: %v", err)
	}
}

func TestLinuxNoAccessSymlinkWithinCredentialWriteGrantFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	allowed := filepath.Join(home, ".ssh", "allowed")
	source := filepath.Join(allowed, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(allowed, "denied")
	if err := os.Symlink(source, entry); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithWritePaths(allowed).WithNoAccessPaths(entry)
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "current", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	_, err = rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}, false)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("credential child write grant left a protected entry mutable: %v", err)
	}
}

func TestLinuxBwrapReservedDirectorySymlinkEscapeRejected(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, directory := range []string{"home", "tmp", "runs", "out", "skills"} {
		t.Run(directory, func(t *testing.T) {
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
			ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			external := t.TempDir()
			dataPath := filepath.Join(external, "data")
			if err := os.WriteFile(dataPath, []byte("ungranted"), 0o600); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(ws.Path, directory)
			if err := os.Remove(entry); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, entry); err != nil {
				t.Fatal(err)
			}
			result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `cat "$1/data"; printf forbidden > "$1/data"`, "test", entry},
			})
			if !isKind(err, ErrPathDenied) {
				t.Fatalf("reserved directory escaped workspace: %#v, %v", result, err)
			}
			data, err := os.ReadFile(dataPath)
			if err != nil || string(data) != "ungranted" {
				t.Fatalf("external resource changed: %q, %v", data, err)
			}
		})
	}
}

func TestLinuxSandboxArgsMaskDefaultCredentialsAndHonorExactGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "cred-mask", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeHost)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasInaccessibleDirMask(args, ssh) {
		t.Fatalf("args = %#v, missing default ~/.ssh mask", args)
	}

	granted := profile.WithReadPaths(ssh)
	grantedArgs, err := rt.linuxSandboxArgs(
		granted,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasInaccessibleDirMask(grantedArgs, ssh) {
		t.Fatalf("granted args = %#v, exact ~/.ssh grant still masked", grantedArgs)
	}
	if !hasArgTriple(grantedArgs, "--ro-bind", ssh, ssh) {
		t.Fatalf("granted args = %#v, missing ~/.ssh read bind", grantedArgs)
	}

	config := filepath.Join(ssh, "config")
	if err := os.WriteFile(config, []byte("Host example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	childArgs, err := rt.linuxSandboxArgs(
		profile.WithReadPaths(config),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDirMaskWithPerms(childArgs, "0111", ssh) {
		t.Fatalf("child grant args = %#v, missing traversable ~/.ssh mask", childArgs)
	}
	if !hasArgTriple(childArgs, "--ro-bind", config, config) {
		t.Fatalf("child grant args = %#v, missing ~/.ssh/config re-bind", childArgs)
	}
	tmpfsAt := argPairIndex(childArgs, "--tmpfs", ssh)
	remountAt := argPairIndex(childArgs, "--remount-ro", ssh)
	bindAt := argTripleIndexAfter(childArgs, tmpfsAt, "--ro-bind", config, config)
	if tmpfsAt < 0 || remountAt < 0 || bindAt < 0 || bindAt > remountAt {
		t.Fatalf("child grant must bind after tmpfs and before remount-ro: %#v", childArgs)
	}
}

func TestLinuxSessionHideArgsOnlyUnderSessionsRoot(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "hide-under-root", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		t.Fatal(err)
	}
	hide, err := rt.linuxSessionHideArgs(WorkspaceWriteProfile(), codeexecutor.Workspace{Path: wsAbs})
	if err != nil {
		t.Fatal(err)
	}
	sessionsRoot, err := filepath.Abs(filepath.Join(rt.root, "sandbox"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(hide, "--tmpfs", sessionsRoot) {
		t.Fatalf("hide args = %#v, want tmpfs over sessions root", hide)
	}

	outside, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hide, err = rt.linuxSessionHideArgs(WorkspaceWriteProfile(), codeexecutor.Workspace{Path: outside})
	if err != nil {
		t.Fatal(err)
	}
	if hide != nil {
		t.Fatalf("hide args = %#v, workspace outside sessions root should not mask siblings", hide)
	}
}

func TestLinuxGrantedSkipsHostHomeMaskAndMountsGrantAncestors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	grantRoot := t.TempDir()
	grant := filepath.Join(grantRoot, "nested", "data")
	if err := os.MkdirAll(grant, 0o755); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "granted-grant", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths(grant)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasInaccessibleDirMask(args, ssh) {
		t.Fatalf("granted args = %#v, must not mask host ~/.ssh", args)
	}
	grantAbs, err := filepath.Abs(grant)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := filepath.Abs(filepath.Join(grantRoot, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(args, "--dir", nested) {
		t.Fatalf("granted args = %#v, missing grant ancestor --dir %s", args, nested)
	}
	if !hasArgTriple(args, "--ro-bind", grantAbs, grantAbs) {
		t.Fatalf("granted args = %#v, missing external grant bind", args)
	}

	dests, err := rt.linuxAbsoluteGrantDests(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(dests, grantAbs) {
		t.Fatalf("linuxAbsoluteGrantDests = %#v, missing %s", dests, grantAbs)
	}

	grantArgs, err := rt.defaultCredentialGrantArgs(profile.WithReadPaths(filepath.Join(ssh, "config")), ws)
	if err != nil {
		t.Fatal(err)
	}
	if grantArgs != nil {
		t.Fatalf("granted credential grant args = %#v, want nil", grantArgs)
	}
}

func TestLinuxGrantedHomeGrantKeepsCredentialMasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(ssh, "config")
	if err := os.WriteFile(config, []byte("Host example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "granted-home-grant", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	sshAbs, err := filepath.Abs(ssh)
	if err != nil {
		t.Fatal(err)
	}
	configAbs, err := filepath.Abs(config)
	if err != nil {
		t.Fatal(err)
	}

	profile := WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths(home)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(args, "--ro-bind", homeAbs, homeAbs) {
		t.Fatalf("granted $HOME grant args = %#v, missing home bind", args)
	}
	if !hasInaccessibleDirMask(args, sshAbs) {
		t.Fatalf("granted $HOME grant args = %#v, missing ~/.ssh mask", args)
	}

	childProfile := profile.WithReadPaths(config)
	childArgs, err := rt.linuxSandboxArgs(
		childProfile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDirMaskWithPerms(childArgs, "0111", sshAbs) {
		t.Fatalf("granted $HOME child grant args = %#v, missing traversable ~/.ssh mask", childArgs)
	}
	tmpfsAt := argPairIndex(childArgs, "--tmpfs", sshAbs)
	remountAt := argPairIndex(childArgs, "--remount-ro", sshAbs)
	bindAt := argTripleIndexAfter(childArgs, tmpfsAt, "--ro-bind", configAbs, configAbs)
	if tmpfsAt < 0 || remountAt < 0 || bindAt < 0 || bindAt > remountAt {
		t.Fatalf("granted $HOME child grant must bind after tmpfs and before remount-ro: %#v", childArgs)
	}

	grantArgs, err := rt.defaultCredentialGrantArgs(childProfile, ws)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(grantArgs, "--ro-bind", configAbs, configAbs) {
		t.Fatalf("granted $HOME child grant args = %#v, missing ~/.ssh/config reopen", grantArgs)
	}

	exact := WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths(ssh)
	exactArgs, err := rt.linuxSandboxArgs(
		exact,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasInaccessibleDirMask(exactArgs, sshAbs) {
		t.Fatalf("granted exact ~/.ssh grant args = %#v, exact grant still masked", exactArgs)
	}
	if !hasArgTriple(exactArgs, "--ro-bind", sshAbs, sshAbs) {
		t.Fatalf("granted exact ~/.ssh grant args = %#v, missing ~/.ssh bind", exactArgs)
	}
}

func TestLinuxCredentialFileMaskWriteGrantAndWorkspaceSkip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	netrc := filepath.Join(home, ".netrc")
	if err := os.WriteFile(netrc, []byte("machine example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(ssh, "config")
	if err := os.WriteFile(config, []byte("Host example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "cred-file-mask", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(WorkspaceWriteProfile().WithReadMode(ReadModeHost), ws); err != nil {
		t.Fatal(err)
	}

	args, err := rt.defaultCredentialDenyMaskArgs(WorkspaceWriteProfile().WithReadMode(ReadModeHost), ws)
	if err != nil {
		t.Fatal(err)
	}
	netrcAbs, err := filepath.Abs(netrc)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(args, "--ro-bind", denyReadMaskSource(ws), netrcAbs) {
		t.Fatalf("mask args = %#v, missing ~/.netrc file mask", args)
	}
	awsAbs, err := filepath.Abs(filepath.Join(home, ".aws"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasInaccessibleDirMask(args, awsAbs) {
		t.Fatalf("mask args = %#v, missing ~/.aws mask", args)
	}

	insideSSH := filepath.Join(ws.Path, ".ssh")
	if err := os.MkdirAll(insideSSH, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ws.Path)
	insideArgs, err := rt.defaultCredentialDenyMaskArgs(WorkspaceWriteProfile().WithReadMode(ReadModeHost), ws)
	if err != nil {
		t.Fatal(err)
	}
	insideAbs, err := filepath.Abs(insideSSH)
	if err != nil {
		t.Fatal(err)
	}
	if hasInaccessibleDirMask(insideArgs, insideAbs) {
		t.Fatalf("mask args = %#v, workspace-local .ssh must not be masked as a host credential", insideArgs)
	}
	t.Setenv("HOME", home)

	writeProfile := WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithWritePaths(config, config)
	writeArgs, err := rt.defaultCredentialGrantArgs(writeProfile, ws)
	if err != nil {
		t.Fatal(err)
	}
	configAbs, err := filepath.Abs(config)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(writeArgs, "--bind", configAbs, configAbs) {
		t.Fatalf("grant args = %#v, missing writable ~/.ssh/config bind", writeArgs)
	}
	grantedMask, err := rt.defaultCredentialDenyMaskArgs(writeProfile, ws)
	if err != nil {
		t.Fatal(err)
	}
	sshAbs, err := filepath.Abs(ssh)
	if err != nil {
		t.Fatal(err)
	}
	sshTmpfs := argPairIndex(grantedMask, "--tmpfs", sshAbs)
	sshRemount := argPairIndex(grantedMask, "--remount-ro", sshAbs)
	awsTmpfs := argPairIndex(grantedMask, "--tmpfs", awsAbs)
	awsRemount := argPairIndex(grantedMask, "--remount-ro", awsAbs)
	bindAt := argTripleIndex(grantedMask, "--bind", configAbs, configAbs)
	if sshTmpfs < 0 || sshRemount < 0 || bindAt < 0 || !(sshTmpfs < bindAt && bindAt < sshRemount) {
		t.Fatalf("mask args = %#v, ~/.ssh/config grant must bind inside the ~/.ssh mask", grantedMask)
	}
	if awsTmpfs >= 0 && awsRemount >= 0 && awsTmpfs < bindAt && bindAt < awsRemount {
		t.Fatalf("mask args = %#v, ~/.ssh/config grant must not bind inside the ~/.aws mask", grantedMask)
	}
	bindCount := 0
	for i := 0; i+2 < len(writeArgs); i++ {
		if writeArgs[i] == "--bind" && writeArgs[i+1] == configAbs && writeArgs[i+2] == configAbs {
			bindCount++
		}
	}
	if bindCount != 1 {
		t.Fatalf("grant args = %#v, duplicate write grant should bind once", writeArgs)
	}

	denied := writeProfile.WithNoAccessPaths(config)
	noneArgs, err := rt.defaultCredentialGrantArgs(denied, ws)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgTriple(noneArgs, "--bind", configAbs, configAbs) ||
		hasArgTriple(noneArgs, "--ro-bind", configAbs, configAbs) {
		t.Fatalf("grant args = %#v, no-access child must not reopen the credential", noneArgs)
	}

	other := t.TempDir()
	otherArgs, err := rt.defaultCredentialGrantArgs(WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithReadPaths(other), ws)
	if err != nil {
		t.Fatal(err)
	}
	otherAbs, err := filepath.Abs(other)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgTriple(otherArgs, "--ro-bind", otherAbs, otherAbs) {
		t.Fatalf("grant args = %#v, non-credential path is not a credential reopen", otherArgs)
	}

	missing := filepath.Join(ssh, "missing-config")
	_, err = rt.defaultCredentialGrantArgs(WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithReadPaths(missing), ws)
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("missing credential child error = %v, want ErrPathDenied", err)
	}

	_, err = rt.defaultCredentialGrantArgs(
		WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithReadPaths(config).WithNoAccessGlobs("["),
		ws,
	)
	if err == nil {
		t.Fatal("invalid no-access glob should fail credential child resolveAccess")
	}
	_, err = rt.defaultCredentialDenyMaskArgs(
		WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithReadPaths(config).WithNoAccessGlobs("["),
		ws,
	)
	if err == nil {
		t.Fatal("invalid no-access glob should fail credential deny mask grants")
	}
}

func TestLinuxCredentialChildGrantPreservesMoreSpecificDeny(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	team := filepath.Join(ssh, "team")
	secret := filepath.Join(team, "private.pem")
	if err := os.MkdirAll(team, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"credential-child-deny",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeHost).
		WithReadPaths(team).
		WithNoAccessPaths(secret)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	sshAbs, err := filepath.Abs(ssh)
	if err != nil {
		t.Fatal(err)
	}
	teamAbs, err := filepath.Abs(team)
	if err != nil {
		t.Fatal(err)
	}
	secretAbs, err := filepath.Abs(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDirMaskWithPerms(args, "0111", sshAbs) {
		t.Fatalf("args = %#v, credential parent must be traversable but not listable", args)
	}
	grantAt := argTripleIndex(args, "--ro-bind", teamAbs, teamAbs)
	denyAt := argTripleIndex(args, "--ro-bind", denyReadMaskSource(ws), secretAbs)
	if grantAt < 0 || denyAt < 0 || denyAt < grantAt {
		t.Fatalf("args = %#v, more-specific deny must follow credential child grant", args)
	}
}

func TestLinuxCredentialDirectorySymlinkMasksResolvedTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credentialDir := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	if err := os.Symlink(credentialDir, ssh); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"credential-symlink",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareProtectedMasks(WorkspaceWriteProfile().WithReadMode(ReadModeHost), ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.defaultCredentialDenyMaskArgs(WorkspaceWriteProfile().WithReadMode(ReadModeHost), ws)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(ssh)
	if err != nil {
		t.Fatal(err)
	}
	if !hasInaccessibleDirMask(args, resolved) {
		t.Fatalf("mask args = %#v, missing mask for resolved credential directory %s", args, resolved)
	}
	if hasArgPair(args, "--tmpfs", ssh) {
		t.Fatalf("mask args = %#v, must not mount directly over credential symlink %s", args, ssh)
	}
}

func TestLinuxCredentialHelpersWhenWorkingDirIsGone(t *testing.T) {
	absWS := t.TempDir()
	if err := os.MkdirAll(filepath.Join(absWS, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := codeexecutor.Workspace{ID: "gone-cwd", Path: absWS}
	rt := NewRuntime(WithWorkspaceRoot("relative-root"))
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths("/usr")
	withDeletedWorkingDir(t)

	if _, err := rt.linuxSandboxArgs(
		WorkspaceWriteProfile(),
		codeexecutor.Workspace{ID: "rel-ws", Path: "relative-ws"},
		"relative-ws/work",
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	); err == nil {
		t.Fatal("linuxSandboxArgs with a relative workspace should fail after cwd is gone")
	}
	if _, err := rt.linuxSandboxArgs(
		WorkspaceWriteProfile(),
		ws,
		filepath.Join(absWS, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	); err == nil {
		t.Fatal("linuxSandboxArgs with a relative runtime root should fail after cwd is gone")
	}
	if _, err := rt.linuxSessionHideArgs(WorkspaceWriteProfile(), codeexecutor.Workspace{Path: absWS}); err == nil {
		t.Fatal("linuxSessionHideArgs with a relative root should fail after cwd is gone")
	}
	if _, err := rt.linuxAbsoluteGrantDests(profile, codeexecutor.Workspace{Path: "relative-ws"}); err == nil {
		t.Fatal("linuxAbsoluteGrantDests with a relative workspace should fail after cwd is gone")
	}
	if _, err := rt.defaultCredentialDenyMaskArgs(WorkspaceWriteProfile(), codeexecutor.Workspace{Path: "relative-ws"}); err == nil {
		t.Fatal("defaultCredentialDenyMaskArgs with a relative workspace should fail after cwd is gone")
	}
	if _, err := rt.defaultCredentialGrantArgs(
		WorkspaceWriteProfile().WithReadPaths("/etc/passwd"),
		codeexecutor.Workspace{Path: "relative-ws"},
	); err == nil {
		t.Fatal("defaultCredentialGrantArgs with a relative workspace should fail after cwd is gone")
	}

	t.Setenv("HOME", "relative-home")
	args, err := rt.defaultCredentialDenyMaskArgs(WorkspaceWriteProfile(), ws)
	if err != nil {
		t.Fatalf("host-root mask with relative HOME: %v", err)
	}
	if hasInaccessibleDirMask(args, "relative-home/.ssh") {
		t.Fatalf("mask args = %#v, relative HOME credential Abs failure should skip the path", args)
	}
	if parent, ok := credentialDenyParent("/tmp/not-a-credential"); ok {
		t.Fatalf("credentialDenyParent = %q, relative HOME Abs failure should not invent a parent", parent)
	}
}

func withDeletedWorkingDir(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs("relative-path"); err == nil {
		t.Skip("filepath.Abs does not fail after deleting the working directory")
	}
}

func TestLinuxSandboxArgsHostRootOmitsHideOutsideSessionsRoot(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	outside := t.TempDir()
	if _, err := codeexecutor.EnsureLayout(outside); err != nil {
		t.Fatal(err)
	}
	ws := codeexecutor.Workspace{ID: "outside-sessions", Path: outside}
	profile := ReadOnlyProfile().WithReadMode(ReadModeHost)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(outside, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionsRoot, err := filepath.Abs(filepath.Join(rt.root, "sandbox"))
	if err != nil {
		t.Fatal(err)
	}
	if hasArgPair(args, "--tmpfs", sessionsRoot) {
		t.Fatalf("args = %#v, workspace outside sessions root should not hide siblings", args)
	}
	outsideAbs, err := filepath.Abs(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(args, "--ro-bind", outsideAbs, outsideAbs) {
		t.Fatalf("args = %#v, missing workspace read baseline", args)
	}
}

func TestLinuxGrantedCredentialMaskWithEmptyHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "granted-empty-home", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeGranted)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--tmpfs" && filepath.Base(args[i+1]) == ".ssh" {
			t.Fatalf("args = %#v, empty HOME must not invent a ~/.ssh mask", args)
		}
	}
	mask, err := rt.defaultCredentialDenyMaskArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat("/etc/shadow"); err == nil {
		if !hasArgTriple(mask, "--ro-bind", denyReadMaskSource(ws), "/etc/shadow") &&
			!hasInaccessibleDirMask(mask, "/etc/shadow") {
			t.Fatalf("mask args = %#v, granted empty-home should still mask /etc/shadow", mask)
		}
	}
}

func TestLinuxCredentialGrantArgsSkipsEmptyRelativeAndWorkspaceChild(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "cred-grant-skips", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ws.Path)
	inside := filepath.Join(ws.Path, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("Host inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	profile := WorkspaceWriteProfile()
	profile.fileSystem.Rules = append(profile.fileSystem.Rules,
		fileSystemRule{Kind: rulePath, Access: accessRead, Path: ""},
		fileSystemRule{Kind: rulePath, Access: accessRead, Path: "relative-cred"},
		fileSystemRule{Kind: ruleGlob, Access: accessNone, Glob: "secrets/*"},
		fileSystemRule{Kind: rulePath, Access: accessRead, Path: inside},
	)
	args, err := rt.defaultCredentialGrantArgs(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	insideAbs, err := filepath.Abs(inside)
	if err != nil {
		t.Fatal(err)
	}
	if hasArgTriple(args, "--ro-bind", insideAbs, insideAbs) {
		t.Fatalf("grant args = %#v, workspace-local credential child must not reopen", args)
	}
	if parent, ok := credentialDenyParent(filepath.Join(ws.Path, ".ssh")); ok {
		t.Fatalf("credentialDenyParent(%q) = %q, exact credential dir is not a child", filepath.Join(ws.Path, ".ssh"), parent)
	}
}

func TestLinuxAbsoluteGrantDestsSkipsWorkspaceAndNoAccess(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "grant-dests", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	blocked := t.TempDir()
	profile := WorkspaceWriteProfile().WithReadMode(ReadModeGranted).
		WithReadPaths(external, filepath.Join(ws.Path, "work"), "relative-grant").
		WithNoAccessPaths(blocked)
	profile.fileSystem.Rules = append(profile.fileSystem.Rules, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: "",
	})
	dests, err := rt.linuxAbsoluteGrantDests(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	externalAbs, err := filepath.Abs(external)
	if err != nil {
		t.Fatal(err)
	}
	blockedAbs, err := filepath.Abs(blocked)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(dests, externalAbs) {
		t.Fatalf("dests = %#v, missing external grant", dests)
	}
	if containsString(dests, blockedAbs) {
		t.Fatalf("dests = %#v, no-access path must not be a grant dest", dests)
	}
	for _, dest := range dests {
		if dest == filepath.Join(ws.Path, "work") || strings.Contains(dest, "relative-grant") {
			t.Fatalf("dests = %#v, workspace or relative grant should be skipped", dests)
		}
	}
}

func TestLinuxDirAncestorsRootAndSeenParents(t *testing.T) {
	if got := appendLinuxDirAncestors(nil, "/", map[string]bool{}); len(got) != 0 {
		t.Fatalf("root dest args = %#v, want none", got)
	}
	if got := appendLinuxDirAncestors(nil, "", map[string]bool{}); len(got) != 0 {
		t.Fatalf("empty dest args = %#v, want none", got)
	}
	seen := map[string]bool{"/": true, "/tmp": true}
	got := appendLinuxDirAncestors(nil, "/tmp/a/b", seen)
	if hasArgPair(got, "--dir", "/tmp") {
		t.Fatalf("args = %#v, already-seen /tmp should not be added", got)
	}
	if !hasArgPair(got, "--dir", "/tmp/a") {
		t.Fatalf("args = %#v, missing unseen ancestor /tmp/a", got)
	}
}

func TestLinuxBwrapHostSecretAndCrossSessionDenied(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshKey := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(sshKey), 0o700); err != nil {
		t.Fatal(err)
	}
	const sshSecret = "ISSUE2605_SSH_SECRET"
	const peerSecret = "ISSUE2605_PEER_SESSION_SECRET"
	if err := os.WriteFile(sshKey, []byte(sshSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	rt := NewRuntime(
		WithWorkspaceRoot(root),
		WithPermissionProfile(
			WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithNetworkPolicy(
				NetworkPolicy{Mode: NetworkEnabled},
			),
		),
	)
	if _, _, err := rt.linuxPreflight(context.Background()); err != nil {
		t.Skipf("bubblewrap preflight unavailable: %v", err)
	}
	peer, err := rt.CreateWorkspace(context.Background(), "peer-session", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	peerFile := filepath.Join(peer.Path, "work", "notes.txt")
	if err := os.WriteFile(peerFile, []byte(peerSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "attacker-session", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	script := `cat "$1" 2>/dev/null || echo SSH_DENIED; ` +
		`ls "$2"; ` +
		`cat "$3" 2>/dev/null || echo PEER_DENIED`
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-c", script, "sandbox-isolation-test",
			sshKey, filepath.Join(root, "sandbox"), peerFile,
		},
	})
	if err != nil {
		t.Fatalf("run error: %v result=%#v", err, res)
	}
	if strings.Contains(res.Stdout, sshSecret) {
		t.Fatalf("host ssh secret leaked:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "SSH_DENIED") {
		t.Fatalf("expected ssh read to fail:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, peerSecret) {
		t.Fatalf("peer session file leaked:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "peer-session") {
		t.Fatalf("sibling session directory visible:\n%s", res.Stdout)
	}

	sshConfig := filepath.Join(home, ".ssh", "config")
	const sshConfigContent = "Host allowed"
	if err := os.WriteFile(sshConfig, []byte(sshConfigContent+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = rt.RunProgram(
		WithAdditionalPermissions(
			context.Background(),
			AdditionalPermissions{ReadPaths: []string{sshConfig}},
		),
		ws,
		codeexecutor.RunProgramSpec{
			Cmd: "bash",
			Args: []string{
				"-c", `cat "$1"; cat "$2" 2>/dev/null || echo KEY_DENIED`,
				"sandbox-credential-grant-test", sshConfig, sshKey,
			},
		},
	)
	if err != nil {
		t.Fatalf("run with credential child grant error: %v result=%#v", err, res)
	}
	if !strings.Contains(res.Stdout, sshConfigContent) {
		t.Fatalf("credential child grant did not expose config:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, sshSecret) || !strings.Contains(res.Stdout, "KEY_DENIED") {
		t.Fatalf("credential child grant leaked sibling key:\n%s", res.Stdout)
	}

	grantedRT := NewRuntime(
		WithWorkspaceRoot(root),
		WithPermissionProfile(
			WorkspaceWriteProfile().WithReadMode(ReadModeHost).WithReadMode(ReadModeGranted).WithNetworkPolicy(
				NetworkPolicy{Mode: NetworkEnabled},
			),
		),
	)
	if _, _, err := grantedRT.linuxPreflight(context.Background()); err != nil {
		t.Skipf("bubblewrap preflight unavailable: %v", err)
	}
	res, err = grantedRT.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-c", `cat /etc/passwd >/dev/null && echo PASSWD_OK; ` +
				`cat "$1" 2>/dev/null || echo ISO_SSH_DENIED; ` +
				`cat "$2" 2>/dev/null || echo ISO_PEER_DENIED`,
			"sandbox-granted-profile-test", sshKey, peerFile,
		},
	})
	if err != nil {
		t.Fatalf("granted run error: %v result=%#v", err, res)
	}
	if !strings.Contains(res.Stdout, "PASSWD_OK") {
		t.Fatalf("granted profile should still read /etc/passwd:\n%s stderr=%s", res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout, sshSecret) || !strings.Contains(res.Stdout, "ISO_SSH_DENIED") {
		t.Fatalf("granted profile leaked host ssh:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, peerSecret) || !strings.Contains(res.Stdout, "ISO_PEER_DENIED") {
		t.Fatalf("granted profile leaked peer session:\n%s", res.Stdout)
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
	if !hasArgSequence(args, "--perms", "000", "--ro-bind-data", "3", missing) {
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
	if !hasArgSequence(args, "--perms", "000", "--ro-bind-data", "3", firstMissing) {
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
	_, err = rt.denyReadMaskSetup(profile, ws, "3")
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
	_, _, err := rt.linuxPreflight(context.Background())
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
	_, _, err := rt.linuxPreflight(context.Background())
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
	path, mountProc, err := rt.linuxPreflight(context.Background())
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
	_, _, err := rt.linuxPreflight(context.Background())
	if !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "generic failure") {
		t.Fatalf("linuxPreflight error = %v, want probe setup failure", err)
	}
}

func TestLinuxCommandForProfileManagedUsesOSSandboxCommand(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	setCachedLinuxPreflight(rt, "/bin/true", false, nil)
	setCachedLinuxRestrictedPreflight(rt, nil)
	ws, err := rt.CreateWorkspace(context.Background(), "linux/command-for-managed", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	cmd, backend, cleanup, err := rt.commandForProfile(
		context.Background(),
		WorkspaceWriteProfile(),
		ws,
		filepath.Join(ws.Path, "work"),
		[]string{"SANDBOX_TEST=1"},
		codeexecutor.RunProgramSpec{Cmd: "/bin/echo", Args: []string{"ok"}},
		sandboxDenialRun{enabled: true, runTag: "TRPC_RUN_test_END_0123456789abcdef_SBX"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if backend != string(BackendLinuxBubblewrap) {
		t.Fatalf("backend = %q, want %q", backend, BackendLinuxBubblewrap)
	}
	if cmd == nil || cmd.Path != "/bin/true" {
		t.Fatalf("cmd = %#v, want fake bwrap command", cmd)
	}
}

func TestLinuxPreflightTransientFailureIsRetried(t *testing.T) {
	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	previousProbe := linuxBasePreflightProbe
	t.Cleanup(func() {
		linuxBasePreflightProbe = previousProbe
	})
	probeCalls := 0
	linuxBasePreflightProbe = func(context.Context, string, bool) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return "", unix.EMFILE
		}
		return "", nil
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if _, _, err := rt.linuxPreflight(context.Background()); !errors.Is(err, unix.EMFILE) {
		t.Fatalf("first linuxPreflight error = %v, want EMFILE", err)
	}
	path, mountProc, err := rt.linuxPreflight(context.Background())
	if err != nil {
		t.Fatalf("second linuxPreflight error = %v, want retry success", err)
	}
	if path != bwrap || !mountProc || probeCalls != 2 {
		t.Fatalf(
			"second linuxPreflight path=%q mountProc=%v calls=%d, want %q true 2",
			path,
			mountProc,
			probeCalls,
			bwrap,
		)
	}
}

func TestLinuxPreflightCancellationAndTimeoutAreNotCached(t *testing.T) {
	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	previousProbe := linuxBasePreflightProbe
	t.Cleanup(func() {
		linuxBasePreflightProbe = previousProbe
	})

	t.Run("callerCancellation", func(t *testing.T) {
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		linuxBasePreflightProbe = func(context.Context, string, bool) (string, error) {
			calls++
			if calls == 1 {
				cancel()
				return "", context.Canceled
			}
			return "", nil
		}

		if _, _, err := rt.linuxPreflight(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("first error = %v, want context.Canceled", err)
		}
		if _, _, err := rt.linuxPreflight(context.Background()); err != nil {
			t.Fatalf("retry after cancellation: %v", err)
		}
		if calls != 2 {
			t.Fatalf("probe calls = %d, want retry after cancellation", calls)
		}
	})

	t.Run("probeDeadline", func(t *testing.T) {
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		calls := 0
		linuxBasePreflightProbe = func(context.Context, string, bool) (string, error) {
			calls++
			if calls == 1 {
				return "", context.DeadlineExceeded
			}
			return "", nil
		}

		if _, _, err := rt.linuxPreflight(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first error = %v, want deadline exceeded", err)
		}
		if _, _, err := rt.linuxPreflight(context.Background()); err != nil {
			t.Fatalf("retry after probe timeout: %v", err)
		}
		if calls != 2 {
			t.Fatalf("probe calls = %d, want retry after timeout", calls)
		}
	})
}

func TestOsSandboxCommandBasePreflightObservesCallerContext(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	previousProbe := linuxBasePreflightProbe
	t.Cleanup(func() {
		linuxBasePreflightProbe = previousProbe
	})

	enabled := WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	runCmd := func(ctx context.Context, rt *Runtime, ws codeexecutor.Workspace) error {
		_, _, cleanup, err := rt.osSandboxCommand(
			ctx,
			enabled,
			ws,
			filepath.Join(ws.Path, "work"),
			nil,
			codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
			sandboxDenialRun{},
		)
		if cleanup != nil {
			cleanup()
		}
		return err
	}

	t.Run("canceledRunDoesNotCacheFailure", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		linuxBasePreflightProbe = func(ctx context.Context, _ string, _ bool) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-release:
					return "", nil
				}
			}
			return "", nil
		}

		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		ws, err := rt.CreateWorkspace(context.Background(), "linux/base-preflight-cancel", codeexecutor.WorkspacePolicy{})
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- runCmd(ctx, rt, ws) }()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("probe did not start")
		}
		cancel()
		select {
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("osSandboxCommand error = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("osSandboxCommand did not observe cancellation")
		}

		if err := runCmd(context.Background(), rt, ws); err != nil {
			t.Fatalf("retry after cancellation: %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("probe calls = %d, want retry after cancellation", got)
		}
	})

	t.Run("concurrentWaiterCancellation", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		linuxBasePreflightProbe = func(ctx context.Context, _ string, _ bool) (string, error) {
			if calls.Add(1) != 1 {
				return "", errors.New("unexpected extra probe")
			}
			close(started)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "", nil
			}
		}

		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		ws, err := rt.CreateWorkspace(context.Background(), "linux/base-preflight-waiter", codeexecutor.WorkspacePolicy{})
		if err != nil {
			t.Fatal(err)
		}

		leaderErr := make(chan error, 1)
		go func() { leaderErr <- runCmd(context.Background(), rt, ws) }()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("leader probe did not start")
		}

		waiterCtx, waiterCancel := context.WithCancel(context.Background())
		waiterErr := make(chan error, 1)
		go func() { waiterErr <- runCmd(waiterCtx, rt, ws) }()
		time.Sleep(50 * time.Millisecond)
		waiterCancel()
		select {
		case err := <-waiterErr:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("waiter error = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter did not observe cancellation")
		}

		close(release)
		select {
		case err := <-leaderErr:
			if err != nil {
				t.Fatalf("leader osSandboxCommand: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("leader did not finish")
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("probe calls = %d, want single in-flight probe", got)
		}
	})
}

func setCachedLinuxRestrictedPreflight(rt *Runtime, err error) {
	rt.restrictedPreflightMu.Lock()
	defer rt.restrictedPreflightMu.Unlock()
	rt.restrictedPreflightReady = true
	rt.restrictedPreflightErr = err
}

func setCachedLinuxPreflight(rt *Runtime, bwrap string, mountProc bool, err error) {
	rt.preflightMu.Lock()
	defer rt.preflightMu.Unlock()
	rt.preflightReady = true
	rt.preflightErr = err
	rt.bwrapPath = bwrap
	rt.bwrapMountProc = mountProc
}

func TestLinuxSandboxCommandBindsDenyReadDataFDAndCleansSyntheticTargets(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	setCachedLinuxPreflight(rt, "/bin/true", false, nil)
	setCachedLinuxRestrictedPreflight(rt, nil)
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
	if len(cmd.ExtraFiles) != 2 {
		t.Fatalf("extra files = %d, want seccomp and deny-read data fds", len(cmd.ExtraFiles))
	}
	if cleanup == nil {
		t.Fatalf("cleanup is nil, want deny-read data fd cleanup")
	}
	if !hasArgPair(cmd.Args, "--seccomp", "3") {
		t.Fatalf("cmd args = %#v, missing --seccomp 3", cmd.Args)
	}
	if !hasArgSequence(cmd.Args, "--perms", "000", "--ro-bind-data", "4", missing) {
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
	for i, f := range cmd.ExtraFiles {
		if _, err := f.Stat(); err == nil {
			t.Fatalf("cleanup did not close extra file %d", i)
		}
	}
}

func TestLinuxSandboxCommandPropagatesSetupError(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	setCachedLinuxPreflight(rt, "/bin/true", false, nil)
	setCachedLinuxRestrictedPreflight(rt, nil)
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
	setCachedLinuxPreflight(rt, "/bin/true", false, nil)
	setCachedLinuxRestrictedPreflight(rt, nil)

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

func TestLinuxManagedRunWithDiagnosticsCoversNoopBackend(t *testing.T) {
	rt, ws := newLinuxDiagnosticsTestRuntime(t, "success")
	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/echo",
	})
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if diagnostics := readDiagnostics(t, diagnosticsCh); len(diagnostics.Denials) != 0 ||
		diagnostics.Truncated {
		t.Fatalf("diagnostics = %#v, want zero value", diagnostics)
	}
}

func TestLinuxManagedRunWithDiagnosticsCanceledContext(t *testing.T) {
	rt, ws := newLinuxDiagnosticsTestRuntime(t, "canceled")
	base, diagnosticsCh := WithDiagnostics(context.Background())
	ctx, cancel := context.WithCancel(base)
	cancel()
	result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/echo",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunProgram error = %v, want context.Canceled", err)
	}
	if result.TimedOut {
		t.Fatalf("TimedOut = true for cancellation: %#v", result)
	}
	_ = readDiagnostics(t, diagnosticsCh)
}

func TestLinuxManagedRunWithDiagnosticsExpiredDeadline(t *testing.T) {
	rt, ws := newLinuxDiagnosticsTestRuntime(t, "deadline")
	base, diagnosticsCh := WithDiagnostics(context.Background())
	ctx, cancel := context.WithDeadline(base, time.Now().Add(-time.Second))
	defer cancel()
	result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/echo",
	})
	if !isKind(err, ErrTimeout) {
		t.Fatalf("RunProgram error = %v, want ErrTimeout", err)
	}
	if !result.TimedOut || result.ExitCode != -1 {
		t.Fatalf("result = %#v, want timed out exit -1", result)
	}
	_ = readDiagnostics(t, diagnosticsCh)
}

func newLinuxDiagnosticsTestRuntime(
	t *testing.T,
	name string,
) (*Runtime, codeexecutor.Workspace) {
	t.Helper()
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	setCachedLinuxPreflight(rt, "/bin/true", false, nil)
	setCachedLinuxRestrictedPreflight(rt, nil)
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"linux/diagnostics-"+name,
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return rt, ws
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasArgPair(args []string, first, second string) bool {
	return argPairIndex(args, first, second) >= 0
}

func argPairIndex(args []string, first, second string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == first && args[i+1] == second {
			return i
		}
	}
	return -1
}

func hasArgTriple(args []string, first, second, third string) bool {
	return argTripleIndex(args, first, second, third) >= 0
}

func argTripleIndex(args []string, first, second, third string) int {
	return argTripleIndexAfter(args, -1, first, second, third)
}

func argTripleIndexAfter(args []string, after int, first, second, third string) int {
	start := after + 1
	if start < 0 {
		start = 0
	}
	for i := start; i+2 < len(args); i++ {
		if args[i] == first && args[i+1] == second && args[i+2] == third {
			return i
		}
	}
	return -1
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
	return hasDirMaskWithPerms(args, "000", target)
}

func hasDirMaskWithPerms(args []string, perms, target string) bool {
	for i := 0; i+3 < len(args); i++ {
		if args[i] == "--perms" &&
			args[i+1] == perms &&
			args[i+2] == "--tmpfs" &&
			args[i+3] == target &&
			argPairIndex(args[i+4:], "--remount-ro", target) >= 0 {
			return true
		}
	}
	return false
}

func TestHasInaccessibleDirMaskRequiresRemountAfterTmpfs(t *testing.T) {
	const target = "/credential"
	if !hasInaccessibleDirMask(
		[]string{"--perms", "000", "--tmpfs", target, "--remount-ro", target},
		target,
	) {
		t.Fatal("valid inaccessible directory mask was not detected")
	}
	if hasInaccessibleDirMask(
		[]string{"--remount-ro", target, "--perms", "000", "--tmpfs", target},
		target,
	) {
		t.Fatal("remount before tmpfs must not be accepted")
	}
}

func TestOpenLinuxSandboxExtraFilesSyntheticOnlyCleanup(t *testing.T) {
	target := filepath.Join(t.TempDir(), "synthetic-mask")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, cleanup, err := openLinuxSandboxExtraFiles(linuxSandboxSetup{
		syntheticDenyReadTargets: []string{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if files != nil {
		t.Fatalf("files = %#v, want nil when only synthetic targets exist", files)
	}
	if cleanup == nil {
		t.Fatal("expected cleanup for synthetic targets")
	}
	cleanup()
	cleanup()
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("synthetic target still present after cleanup: %v", err)
	}
}

func TestOpenLinuxSandboxExtraFilesEmptySetup(t *testing.T) {
	files, cleanup, err := openLinuxSandboxExtraFiles(linuxSandboxSetup{})
	if err != nil {
		t.Fatal(err)
	}
	if files != nil || cleanup != nil {
		t.Fatalf("files=%v cleanup=%v, want both nil", files, cleanup)
	}
}

func TestOpenLinuxSandboxExtraFilesSeccompAndDenyRead(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	files, cleanup, err := openLinuxSandboxExtraFiles(linuxSandboxSetup{
		needsSeccompFD:      true,
		needsDenyReadDataFD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files=%d, want 2", len(files))
	}
	if cleanup == nil {
		t.Fatal("expected cleanup")
	}
	cleanup()
	cleanup()
	for i, f := range files {
		if err := f.Close(); err == nil {
			t.Fatalf("file %d still closable after cleanup", i)
		}
	}
}

func TestOpenLinuxSandboxExtraFilesSeccompFailsClosed(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	target := filepath.Join(t.TempDir(), "synthetic")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	withExhaustedFileDescriptors(t, func() {
		files, cleanup, err := openLinuxSandboxExtraFiles(linuxSandboxSetup{
			needsSeccompFD:           true,
			syntheticDenyReadTargets: []string{target},
		})
		if err == nil || files != nil || cleanup != nil {
			t.Fatalf("files=%v cleanup=%v err=%v, want seccomp open failure", files, cleanup, err)
		}
	})
}

func TestOpenLinuxSandboxExtraFilesDenyReadFailsClosesSeccomp(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Skip(err)
	}
	old := lim
	lim.Cur = 64
	if lim.Cur > lim.Max {
		lim.Cur = lim.Max
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Skip(err)
	}
	defer func() { _ = unix.Setrlimit(unix.RLIMIT_NOFILE, &old) }()

	held := make([]*os.File, 0, 64)
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for {
		f, err := os.Open("/dev/null")
		if err != nil {
			break
		}
		held = append(held, f)
	}
	if len(held) == 0 {
		t.Skip("could not exhaust file descriptors")
	}
	// Free a single slot for seccomp memfd; deny-read /dev/null should then fail.
	_ = held[len(held)-1].Close()
	held = held[:len(held)-1]

	files, cleanup, err := openLinuxSandboxExtraFiles(linuxSandboxSetup{
		needsSeccompFD:      true,
		needsDenyReadDataFD: true,
	})
	if err == nil || files != nil || cleanup != nil {
		t.Fatalf("files=%v cleanup=%v err=%v, want deny-read open failure after seccomp", files, cleanup, err)
	}
	// closeAll() must have closed the seccomp memfd so this last FD slot works.
	reopen, reopenErr := os.Open("/dev/null")
	if reopenErr != nil {
		t.Fatalf("expected /dev/null reopen after closeAll, got %v", reopenErr)
	}
	_ = reopen.Close()
}

func TestOpenSeccompFilterMemfdFailsClosedOnEMFILE(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	withExhaustedFileDescriptors(t, func() {
		if _, err := openSeccompFilterMemfd(); err == nil {
			t.Fatal("expected memfd create failure under exhausted FD table")
		}
	})
}

func withExhaustedFileDescriptors(t *testing.T, fn func()) {
	t.Helper()
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Skip(err)
	}
	old := lim
	// Keep the ceiling low so exhausting FDs is cheap and reliable.
	const soft = 64
	lim.Cur = soft
	if lim.Cur > lim.Max {
		lim.Cur = lim.Max
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Skip(err)
	}
	defer func() { _ = unix.Setrlimit(unix.RLIMIT_NOFILE, &old) }()

	held := make([]*os.File, 0, soft)
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for {
		f, err := os.Open("/dev/null")
		if err != nil {
			break
		}
		held = append(held, f)
	}
	if len(held) == 0 {
		t.Skip("could not exhaust file descriptors")
	}
	fn()
}

func TestLinuxRestrictedPreflightFailsClosedOnBadBwrap(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	release, err := currentKernelRelease()
	if err != nil {
		t.Skip(err)
	}
	if err := kernelSupportsRestrictedSeccomp(release); err != nil {
		t.Skip(err)
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	err = rt.linuxRestrictedPreflight(context.Background(), "/bin/false", true)
	if err == nil {
		t.Fatal("expected restricted preflight failure")
	}
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("err = %v, want ErrSetupFailed", err)
	}
	if !strings.Contains(err.Error(), "restricted AF_UNIX seccomp preflight failed") {
		t.Fatalf("err = %v, want seccomp preflight hint", err)
	}
	// Cached failure remains fail-closed.
	if err2 := rt.linuxRestrictedPreflight(
		context.Background(),
		"/usr/local/bin/bwrap",
		true,
	); err2 == nil {
		t.Fatal("expected cached restricted preflight failure")
	}
}

func TestLinuxSandboxCommandFailsClosedWhenRestrictedPreflightCached(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	setCachedLinuxPreflight(rt, "/bin/true", true, nil)
	forced := backendError(ErrSetupFailed, string(BackendLinuxBubblewrap), errors.New("forced restricted preflight failure"))
	setCachedLinuxRestrictedPreflight(rt, forced)
	ws, err := rt.CreateWorkspace(context.Background(), "restricted-preflight-cached", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = rt.osSandboxCommand(
		context.Background(),
		WorkspaceWriteProfile(),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		sandboxDenialRun{},
	)
	if err == nil {
		t.Fatal("expected osSandboxCommand to fail closed")
	}
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("err = %v, want ErrSetupFailed", err)
	}
}

func TestRunBwrapSeccompPreflightProbeRejectsInvalidBinary(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	filter, err := openSeccompFilterMemfd()
	if err != nil {
		t.Skipf("open seccomp memfd unavailable: %v", err)
	}
	defer filter.Close()
	stderr, err := runBwrapSeccompPreflightProbe(
		context.Background(),
		"/bin/false",
		true,
		filter,
	)
	if err == nil {
		t.Fatal("expected probe failure")
	}
	_ = stderr
}

func TestLinuxRestrictedPreflightFailClosedBranches(t *testing.T) {
	restore := func(
		native func() (seccompArchPolicy, error),
		kernel func() (string, error),
		openMemfd func() (*os.File, error),
		probe func(context.Context, string, bool, *os.File) (string, error),
	) {
		linuxNativeSeccompPolicy = native
		linuxKernelRelease = kernel
		linuxOpenSeccompMemfd = openMemfd
		linuxSeccompProbe = probe
	}
	defer restore(
		nativeSeccompPolicy,
		currentKernelRelease,
		openSeccompFilterMemfd,
		runBwrapSeccompPreflightProbe,
	)

	// Fixed successes so each subtest only exercises its intended fail-closed
	// branch, independent of GOARCH and host kernel version.
	fixedPolicy := func() (seccompArchPolicy, error) {
		return seccompPolicyAMD64, nil
	}
	supportedKernel := func() (string, error) {
		return "5.15.0", nil
	}
	unusedMemfd := func() (*os.File, error) {
		t.Fatal("openSeccompFilterMemfd should not run in this branch")
		return nil, nil
	}
	unusedProbe := func(context.Context, string, bool, *os.File) (string, error) {
		t.Fatal("seccomp probe should not run in this branch")
		return "", nil
	}

	t.Run("unsupportedArch", func(t *testing.T) {
		linuxNativeSeccompPolicy = func() (seccompArchPolicy, error) {
			return seccompArchPolicy{}, errors.New("forced unsupported arch")
		}
		linuxKernelRelease = supportedKernel
		linuxOpenSeccompMemfd = unusedMemfd
		linuxSeccompProbe = unusedProbe
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		err := rt.linuxRestrictedPreflight(context.Background(), "/bin/true", true)
		if err == nil || !isKind(err, ErrUnsupportedBackend) {
			t.Fatalf("err=%v, want ErrUnsupportedBackend", err)
		}
	})

	t.Run("kernelReleaseError", func(t *testing.T) {
		linuxNativeSeccompPolicy = fixedPolicy
		linuxKernelRelease = func() (string, error) {
			return "", errors.New("forced uname failure")
		}
		linuxOpenSeccompMemfd = unusedMemfd
		linuxSeccompProbe = unusedProbe
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		err := rt.linuxRestrictedPreflight(context.Background(), "/bin/true", true)
		if err == nil || !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "read kernel release") {
			t.Fatalf("err=%v, want kernel release setup failure", err)
		}
	})

	t.Run("kernelTooOld", func(t *testing.T) {
		linuxNativeSeccompPolicy = fixedPolicy
		linuxKernelRelease = func() (string, error) { return "4.7.0", nil }
		linuxOpenSeccompMemfd = unusedMemfd
		linuxSeccompProbe = unusedProbe
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		err := rt.linuxRestrictedPreflight(context.Background(), "/bin/true", true)
		if err == nil || !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "below required") {
			t.Fatalf("err=%v, want old-kernel setup failure", err)
		}
	})

	t.Run("memfdOpenError", func(t *testing.T) {
		linuxNativeSeccompPolicy = fixedPolicy
		linuxKernelRelease = supportedKernel
		linuxOpenSeccompMemfd = func() (*os.File, error) {
			return nil, errors.New("forced memfd failure")
		}
		linuxSeccompProbe = unusedProbe
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		err := rt.linuxRestrictedPreflight(context.Background(), "/bin/true", true)
		if err == nil || !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "forced memfd failure") {
			t.Fatalf("err=%v, want memfd setup failure", err)
		}
	})
}

func TestLinuxRestrictedPreflightCancellationAndTimeoutAreNotCached(t *testing.T) {
	origNative := linuxNativeSeccompPolicy
	origKernel := linuxKernelRelease
	origOpen := linuxOpenSeccompMemfd
	origProbe := linuxSeccompProbe
	defer func() {
		linuxNativeSeccompPolicy = origNative
		linuxKernelRelease = origKernel
		linuxOpenSeccompMemfd = origOpen
		linuxSeccompProbe = origProbe
	}()

	linuxNativeSeccompPolicy = func() (seccompArchPolicy, error) {
		return seccompPolicyAMD64, nil
	}
	linuxKernelRelease = func() (string, error) {
		return "5.15.0", nil
	}
	linuxOpenSeccompMemfd = func() (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "seccomp-preflight")
	}

	t.Run("callerCancellation", func(t *testing.T) {
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		linuxSeccompProbe = func(
			context.Context,
			string,
			bool,
			*os.File,
		) (string, error) {
			calls++
			if calls == 1 {
				cancel()
				return "", context.Canceled
			}
			return "", nil
		}

		if err := rt.linuxRestrictedPreflight(ctx, "/bin/true", true); !errors.Is(err, context.Canceled) {
			t.Fatalf("first error = %v, want context.Canceled", err)
		}
		if err := rt.linuxRestrictedPreflight(
			context.Background(),
			"/bin/true",
			true,
		); err != nil {
			t.Fatalf("retry after cancellation: %v", err)
		}
		if calls != 2 {
			t.Fatalf("probe calls = %d, want retry after cancellation", calls)
		}
	})

	t.Run("probeDeadline", func(t *testing.T) {
		rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
		calls := 0
		linuxSeccompProbe = func(
			context.Context,
			string,
			bool,
			*os.File,
		) (string, error) {
			calls++
			if calls == 1 {
				return "", context.DeadlineExceeded
			}
			return "", nil
		}

		err := rt.linuxRestrictedPreflight(
			context.Background(),
			"/bin/true",
			true,
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first error = %v, want deadline exceeded", err)
		}
		if err := rt.linuxRestrictedPreflight(
			context.Background(),
			"/bin/true",
			true,
		); err != nil {
			t.Fatalf("retry after probe timeout: %v", err)
		}
		if calls != 2 {
			t.Fatalf("probe calls = %d, want retry after timeout", calls)
		}
	})
}

func TestLinuxRestrictedPreflightEMFILEIsRetried(t *testing.T) {
	origNative := linuxNativeSeccompPolicy
	origKernel := linuxKernelRelease
	origOpen := linuxOpenSeccompMemfd
	origProbe := linuxSeccompProbe
	defer func() {
		linuxNativeSeccompPolicy = origNative
		linuxKernelRelease = origKernel
		linuxOpenSeccompMemfd = origOpen
		linuxSeccompProbe = origProbe
	}()

	linuxNativeSeccompPolicy = func() (seccompArchPolicy, error) {
		return seccompPolicyAMD64, nil
	}
	linuxKernelRelease = func() (string, error) {
		return "5.15.0", nil
	}
	openCalls := 0
	linuxOpenSeccompMemfd = func() (*os.File, error) {
		openCalls++
		if openCalls == 1 {
			return nil, unix.EMFILE
		}
		return os.CreateTemp(t.TempDir(), "seccomp-preflight-retry")
	}
	linuxSeccompProbe = func(
		context.Context,
		string,
		bool,
		*os.File,
	) (string, error) {
		return "", nil
	}

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	err := rt.linuxRestrictedPreflight(
		context.Background(),
		"/bin/true",
		true,
	)
	if !errors.Is(err, unix.EMFILE) {
		t.Fatalf("first error = %v, want EMFILE", err)
	}
	if err := rt.linuxRestrictedPreflight(
		context.Background(),
		"/bin/true",
		true,
	); err != nil {
		t.Fatalf("retry after EMFILE = %v, want success", err)
	}
	if openCalls != 2 {
		t.Fatalf("memfd open calls = %d, want retry", openCalls)
	}
}

func TestOsSandboxCommandFailsWhenExtraFilesOpenFails(t *testing.T) {
	requireLinuxBwrap(t)
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "extrafiles-fail", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	origExtra := linuxOpenExtraFiles
	linuxOpenExtraFiles = func(linuxSandboxSetup) ([]*os.File, commandCleanup, error) {
		return nil, nil, backendError(ErrSetupFailed, string(BackendLinuxBubblewrap), errors.New("forced extra files failure"))
	}
	defer func() { linuxOpenExtraFiles = origExtra }()

	profile := WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	_, _, _, err = rt.osSandboxCommand(
		context.Background(),
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		sandboxDenialRun{},
	)
	if err == nil || !isKind(err, ErrSetupFailed) || !strings.Contains(err.Error(), "forced extra files failure") {
		t.Fatalf("err=%v, want forced ExtraFiles setup failure", err)
	}
}

func TestRunBwrapSeccompPreflightProbeTimeout(t *testing.T) {
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	filter, err := openSeccompFilterMemfd()
	if err != nil {
		t.Skip(err)
	}
	defer filter.Close()

	script := filepath.Join(t.TempDir(), "slow-bwrap.sh")
	// exec so CommandContext cancellation kills the sleeper directly.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := bwrapSeccompPreflightTimeout
	bwrapSeccompPreflightTimeout = 50 * time.Millisecond
	defer func() { bwrapSeccompPreflightTimeout = orig }()

	_, err = runBwrapSeccompPreflightProbe(
		context.Background(),
		script,
		false,
		filter,
	)
	if err == nil {
		t.Fatal("expected timeout failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(strings.ToLower(err.Error()), "kill") {
		t.Fatalf("err=%v, want deadline/kill from CommandContext", err)
	}
}

func TestLinuxGrantedReadOnlyWorkspace(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "readonly-granted", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := ReadOnlyProfile().WithReadMode(ReadModeGranted)
	if err := rt.prepareProtectedMasks(profile, ws); err != nil {
		t.Fatal(err)
	}
	args, err := rt.linuxSandboxArgs(profile, ws, filepath.Join(ws.Path, "work"), nil, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgTriple(args, "--ro-bind", ws.Path, ws.Path) {
		t.Fatalf("missing read-only workspace: %v", args)
	}
	if hasArgTriple(args, "--bind", ws.Path, ws.Path) {
		t.Fatalf("read-only workspace became writable: %v", args)
	}
	if hasArgTriple(args, "--ro-bind-try", "/etc", "/etc") {
		t.Fatalf("entire /etc is exposed: %v", args)
	}
	// Optional runtime grants are skipped when the host lacks them.
	for _, path := range []string{"/etc/passwd", "/etc/ssl/certs", "/etc/alternatives", "/etc/services"} {
		_, statErr := os.Stat(path)
		if got := hasArgTriple(args, "--ro-bind-try", path, path); got != (statErr == nil) {
			t.Fatalf("runtime path %s bound=%v, host present=%v", path, got, statErr == nil)
		}
	}
}

func TestLinuxBwrapGrantedReadOnlyIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{".env", "server.pem", "server.key"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("HOST_SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(ReadOnlyProfile().WithReadMode(ReadModeGranted)))
	if _, _, err := rt.linuxPreflight(context.Background()); err != nil {
		t.Skipf("bubblewrap unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "readonly", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "input.txt"), []byte("WORKSPACE_INPUT"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
cat input.txt
if touch output.txt 2>/dev/null; then exit 21; fi
for name in .env server.pem server.key; do
  if cat "$1/$name" 2>/dev/null; then exit 22; fi
done
if cat /etc/shadow >/dev/null 2>&1; then exit 23; fi
if test -e /etc/hostname; then exit 24; fi
cat /etc/passwd >/dev/null
printf '\nISOLATION_OK\n'
`
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{"-c", script, "test", home}})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "WORKSPACE_INPUT") || !strings.Contains(res.Stdout, "ISOLATION_OK") {
		t.Fatalf("granted read-only run: result=%#v error=%v", res, err)
	}
}

func TestLinuxBwrapNestedSessionScopes(t *testing.T) {
	profiles := map[string]PermissionProfile{
		"host-root":    WorkspaceWriteProfile().WithReadMode(ReadModeHost),
		"no-host-root": WorkspaceWriteProfile().WithReadMode(ReadModeGranted),
	}
	for name, profile := range profiles {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
			if _, _, err := rt.linuxPreflight(ctx); err != nil {
				t.Skipf("bubblewrap unavailable: %v", err)
			}
			workspaces := make([]codeexecutor.Workspace, 0, 3)
			for _, id := range []string{"app", "app/user/first", "app/user/second"} {
				ws, err := rt.CreateWorkspace(ctx, id, codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: "work/data.txt", Content: []byte(id)}}); err != nil {
					t.Fatal(err)
				}
				workspaces = append(workspaces, ws)
			}
			parentFile := filepath.Join(workspaces[0].Path, "work", "data.txt")
			childFile := filepath.Join(workspaces[1].Path, "work", "data.txt")
			siblingFile := filepath.Join(workspaces[2].Path, "work", "data.txt")
			res, err := rt.RunProgram(ctx, workspaces[0], codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `test -r "$1" && test -r "$2" && printf '%s' parent-update > "$1"`, "test", childFile, siblingFile},
			})
			if err != nil || res.ExitCode != 0 {
				t.Fatalf("parent access: %#v, %v", res, err)
			}
			res, err = rt.RunProgram(ctx, workspaces[1], codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `test ! -r "$1" && test ! -r "$2" && cat data.txt`, "test", parentFile, siblingFile},
			})
			if err != nil || res.ExitCode != 0 || res.Stdout != "parent-update" {
				t.Fatalf("child isolation: %#v, %v", res, err)
			}
		})
	}
}

func TestLinuxBwrapProtectedAncestorRename(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, protection := range []string{"credential", "session"} {
			t.Run(string(mode)+"/"+protection, func(t *testing.T) {
				parent := t.TempDir()
				ancestor := filepath.Join(parent, "original")
				if err := os.MkdirAll(ancestor, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HOME", ancestor)
				root := t.TempDir()
				if protection == "session" {
					root = ancestor
				}
				rt := NewRuntime(WithWorkspaceRoot(root), WithPermissionProfile(
					WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(parent),
				))
				secretRel := filepath.Join(".ssh", "id_rsa")
				if protection == "session" {
					peer, err := rt.CreateWorkspace(ctx, "peer", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					secretRel, err = filepath.Rel(ancestor, filepath.Join(peer.Path, "work", "secret"))
					if err != nil {
						t.Fatal(err)
					}
				}
				secretPath := filepath.Join(ancestor, secretRel)
				if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(secretPath, []byte("PROTECTED_RENAME_SECRET"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(ancestor, "public"), []byte("PUBLIC"), 0o600); err != nil {
					t.Fatal(err)
				}
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				script := `target=$1
if mv "$1" "$2" 2>/dev/null; then target=$2; fi
cat "$target/public" || exit 20
if cat "$target/$3" 2>/dev/null; then exit 21; fi
printf '_DENIED'
`
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", script, "test", ancestor, filepath.Join(parent, "moved"), secretRel},
				})
				if err != nil || result.ExitCode != 0 || result.Stdout != "PUBLIC_DENIED" {
					t.Fatalf("protection after ancestor rename: %#v, %v", result, err)
				}
			})
		}
	}
}

func TestLinuxBwrapDefaultPythonRuntime(t *testing.T) {
	requireReadModeBackend(t)
	const python = "/usr/bin/python3"
	if _, err := os.Stat(python); err != nil {
		t.Skip("system python3 unavailable")
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "python", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, ssl, tempfile
path = pathlib.Path("result.json")
path.write_text(json.dumps({"answer": 42}))
with tempfile.TemporaryFile() as temp:
    temp.write(b"temporary data")
assert ssl.create_default_context().verify_mode == ssl.CERT_REQUIRED
print(json.loads(path.read_text())["answer"])
`
	result, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: python, Args: []string{"-c", script},
	})
	if err != nil || result.ExitCode != 0 || result.Stdout != "42\n" {
		t.Fatalf("default Python runtime: %#v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, "work", "result.json"))
	if err != nil || string(data) != `{"answer": 42}` {
		t.Fatalf("Python workspace output = %q, %v", data, err)
	}
}

func (r *Runtime) linuxSandboxArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
	mountProc bool,
) ([]string, error) {
	setup, err := r.linuxSandboxSetup(profile, ws, cwd, env, spec, mountProc)
	if err != nil {
		return nil, err
	}
	return setup.args, nil
}

func (r *Runtime) denyReadMaskArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	setup, err := r.denyReadMaskSetup(profile, ws, "3")
	if err != nil {
		return nil, err
	}
	return setup.args, nil
}
