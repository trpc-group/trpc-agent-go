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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestReadModeDefaultAndValidation(t *testing.T) {
	ctx := context.Background()
	for _, profile := range []PermissionProfile{{}, ReadOnlyProfile(), WorkspaceWriteProfile(), WorkspaceWriteProfile().WithReadMode("")} {
		if profile.effectiveReadMode() != ReadModeGranted || normalizeProfile(profile).fileSystem.ReadMode != ReadModeGranted {
			t.Fatal("unset mode must normalize to granted")
		}
	}
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(WorkspaceWriteProfile().WithReadMode("unknown")))
	if _, err := rt.CreateWorkspace(ctx, "invalid", codeexecutor.WorkspacePolicy{}); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	ws := codeexecutor.Workspace{Path: t.TempDir()}
	if _, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "/bin/true"}); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("RunProgram: %v", err)
	}
	if _, err := rt.StartProcess(ctx, ws, ProcessSpec{Cmd: "/bin/true"}); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("StartProcess: %v", err)
	}
	if err := rt.StageInputs(ctx, ws, nil); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("StageInputs: %v", err)
	}
	if err := rt.PutFiles(ctx, ws, nil); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("PutFiles: %v", err)
	}
	if _, err := rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{}); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("CollectOutputs: %v", err)
	}
	if _, err := rt.Collect(ctx, ws, nil); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("Collect: %v", err)
	}
	for _, source := range []string{t.TempDir(), "relative"} {
		if err := rt.StageDirectory(ctx, ws, source, "work/input", codeexecutor.StageOptions{}); !isKind(err, ErrPolicyViolation) {
			t.Fatalf("StageDirectory(%s): %v", source, err)
		}
	}
}

func TestReadModeStagingAndProtections(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	credential := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(credential), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{credential, filepath.Join(home, "public.txt")} {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlink aliases unavailable: %v", err)
	}
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		t.Run(string(mode), func(t *testing.T) {
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(WorkspaceWriteProfile().WithReadMode(mode)))
			ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			peer, err := rt.CreateWorkspace(ctx, "peer", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			public := filepath.Join(home, "public.txt")
			err = rt.StageDirectory(ctx, ws, public, "work/public", codeexecutor.StageOptions{})
			if mode == ReadModeGranted && !isKind(err, ErrPathDenied) {
				t.Fatalf("ungranted source: %v", err)
			}
			if mode == ReadModeHost && err != nil {
				t.Fatal(err)
			}
			parentGrant := WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{alias, rt.root}})
			if err := rt.StageDirectory(parentGrant, ws, public, "work/allowed", codeexecutor.StageOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{credential, filepath.Join(alias, ".ssh", "config"), peer.Path, home} {
				if err := rt.StageDirectory(parentGrant, ws, source, "work/blocked", codeexecutor.StageOptions{}); !isKind(err, ErrPathDenied) {
					t.Fatalf("protected %s: %v", source, err)
				}
			}
			exactGrant := WithAdditionalPermissions(parentGrant, AdditionalPermissions{ReadPaths: []string{credential}})
			if err := rt.StageDirectory(exactGrant, ws, credential, "work/config", codeexecutor.StageOptions{}); err != nil {
				t.Fatal(err)
			}
			denied := WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{home}})
			rt.profile = rt.profile.WithNoAccessPaths(public)
			if err := rt.StageDirectory(denied, ws, public, "work/denied", codeexecutor.StageOptions{}); !isKind(err, ErrPathDenied) {
				t.Fatalf("explicit deny: %v", err)
			}
		})
	}
}

func TestReadModeWorkspaceStagingChecksDescendants(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, scheme := range []string{"workspace", "skill"} {
			for _, glob := range []bool{false, true} {
				name := string(mode) + "/" + scheme + "/path"
				if glob {
					name = string(mode) + "/" + scheme + "/glob"
				}
				t.Run(name, func(t *testing.T) {
					source, from := "work/source", "workspace://work/source"
					if scheme == "skill" {
						source, from = "skills/team", "skill://team"
					}
					secret := source + "/private/secret"
					profile := WorkspaceWriteProfile().WithReadMode(mode)
					if glob {
						profile = profile.WithNoAccessGlobs(source + "/private/**")
					} else {
						profile = profile.WithNoAccessPaths(secret)
					}
					rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(ws.Path, filepath.FromSlash(secret))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
						t.Fatal(err)
					}
					err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{From: from, To: "work/copied"}})
					if !isKind(err, ErrPathDenied) {
						t.Fatalf("denied descendant staged: %v", err)
					}
					if _, err := os.Stat(filepath.Join(ws.Path, "work", "copied", "private", "secret")); !os.IsNotExist(err) {
						t.Fatalf("denied data copied to a readable path: %v", err)
					}
				})
			}
		}
	}
}

func TestReadModeWorkspaceAliasReadDenials(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, glob := range []bool{false, true} {
			name := string(mode) + "/path"
			if glob {
				name = string(mode) + "/glob"
			}
			t.Run(name, func(t *testing.T) {
				profile := WorkspaceWriteProfile().WithReadMode(mode)
				if glob {
					profile = profile.WithNoAccessGlobs("work/source/private/**")
				} else {
					profile = profile.WithNoAccessPaths("work/source/private/secret")
				}
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				secret := filepath.Join(ws.Path, "work", "source", "private", "secret")
				if err := os.MkdirAll(filepath.Dir(secret), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(ws.Path, "work", "source"), filepath.Join(ws.Path, "work", "alias")); err != nil {
					t.Skipf("symlink aliases unavailable: %v", err)
				}
				alias := "work/alias/private/secret"
				if _, err := rt.Collect(ctx, ws, []string{alias}); !isKind(err, ErrPathDenied) {
					t.Fatalf("Collect read a denied resource through its parent alias: %v", err)
				}
				err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{From: "workspace://" + alias, To: "work/copied"}})
				if !isKind(err, ErrPathDenied) {
					t.Fatalf("StageInputs copied a denied resource through its parent alias: %v", err)
				}
				if err := rt.StageDirectory(ctx, ws, filepath.Join(ws.Path, alias), "work/copied", codeexecutor.StageOptions{}); !isKind(err, ErrPathDenied) {
					t.Fatalf("StageDirectory copied a denied resource through its parent alias: %v", err)
				}
				if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: alias, Content: []byte("changed")}}); !isKind(err, ErrPathDenied) {
					t.Fatalf("PutFiles wrote a denied resource through its parent alias: %v", err)
				}
			})
		}
	}
}

func requireReadModeBackend(t *testing.T) {
	t.Helper()
	program := "sandbox-exec"
	if runtime.GOOS == "linux" {
		program = "bwrap"
	} else if runtime.GOOS != "darwin" {
		t.Skip("native sandbox requires Linux or macOS")
	}
	if _, err := exec.LookPath(program); err != nil {
		t.Skipf("%s unavailable", program)
	}
	if runtime.GOOS == "linux" {
		if err := exec.Command(program, "--unshare-user", "--ro-bind", "/", "/", "--", "/bin/true").Run(); err != nil {
			t.Skipf("bubblewrap user namespaces unavailable: %v", err)
		}
	}
}

func TestReadModeNativeWorkspaceRootAlias(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, base := range []PermissionProfile{ReadOnlyProfile(), WorkspaceWriteProfile()} {
		for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
			t.Run(string(mode)+"/"+string(base.fileSystem.Rules[len(base.fileSystem.Rules)-1].Access), func(t *testing.T) {
				root := t.TempDir()
				alias := filepath.Join(t.TempDir(), "workspace-root")
				if err := os.Symlink(root, alias); err != nil {
					t.Skipf("symlink aliases unavailable: %v", err)
				}
				rt := NewRuntime(WithWorkspaceRoot(alias), WithPermissionProfile(base.WithReadMode(mode)))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(ws.Path, "work", "input")
				if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
					t.Fatal(err)
				}
				script, want := `cat "$1" && test ! -w "$1"`, "data"
				if base.fileSystem.Rules[len(base.fileSystem.Rules)-1].Access == accessWrite {
					script, want = `printf changed > "$1" && cat "$1"`, "changed"
				}
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{"-c", script, "test", path}})
				if err != nil || result.ExitCode != 0 || result.Stdout != want {
					t.Fatalf("workspace grant lost through root alias: %#v, %v", result, err)
				}
			})
		}
	}
}

func TestReadModeNativeReads(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	host := t.TempDir()
	public := filepath.Join(host, "public.txt")
	secret := filepath.Join(host, "denied.txt")
	for _, path := range []string{public, secret} {
		if err := os.WriteFile(path, []byte("host"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range []PermissionProfile{ReadOnlyProfile(), WorkspaceWriteProfile()} {
		for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
			t.Run(string(profile.enforcement())+"/"+string(mode)+"/"+string(profile.fileSystem.Rules[len(profile.fileSystem.Rules)-1].Access), func(t *testing.T) {
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile.WithReadMode(mode).WithNoAccessPaths(secret)))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				script := `test ! -r "$2" && if test -r "$1"; then printf readable; else printf hidden; fi`
				spec := codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{"-c", script, "test", public, secret}}
				result, err := rt.RunProgram(ctx, ws, spec)
				if err != nil {
					t.Fatal(err)
				}
				want := "hidden"
				if mode == ReadModeHost {
					want = "readable"
				}
				if result.ExitCode != 0 || result.Stdout != want {
					t.Fatalf("mode read: %#v, want %s", result, want)
				}
				grant := WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{host}})
				process, err := rt.StartProcess(grant, ws, ProcessSpec{Cmd: spec.Cmd, Args: spec.Args})
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(process.Stdout())
				if err != nil {
					t.Fatal(err)
				}
				if err := process.Wait(); err != nil || string(data) != "readable" {
					t.Fatalf("granted process: %q, %v", data, err)
				}
			})
		}
	}
}

func TestReadModeNativeDeniedParentChildGrant(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		t.Run(string(mode), func(t *testing.T) {
			parent := t.TempDir()
			allowed, sibling := filepath.Join(parent, "allowed"), filepath.Join(parent, "sibling")
			for _, path := range []string{allowed, sibling} {
				if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			profile := WorkspaceWriteProfile().WithReadMode(mode).WithNoAccessPaths(parent).WithReadPaths(allowed)
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
			ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `cat "$1" && test ! -r "$2"`, "test", allowed, sibling},
			})
			if runtime.GOOS == "linux" {
				if !isKind(err, ErrPolicyViolation) {
					t.Fatalf("unsupported child restoration must fail explicitly: %#v, %v", result, err)
				}
				return
			}
			if err != nil || result.ExitCode != 0 || result.Stdout != "data" {
				t.Fatalf("more-specific grant should restore only the child: %#v, %v", result, err)
			}
		})
	}
}

func TestReadModeNativeBuilderOrder(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	host := t.TempDir()
	allowed := filepath.Join(host, "allowed")
	ungranted := filepath.Join(host, "ungranted")
	denied := filepath.Join(host, "denied")
	writable := filepath.Join(host, "writable")
	for _, path := range []string{allowed, ungranted, denied, writable} {
		if err := os.WriteFile(path, []byte("host"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, base := range []PermissionProfile{ReadOnlyProfile(), WorkspaceWriteProfile()} {
			for _, modeFirst := range []bool{true, false} {
				name := string(mode) + "/" + string(base.fileSystem.Rules[len(base.fileSystem.Rules)-1].Access)
				if modeFirst {
					name += "/mode-first"
				} else {
					name += "/mode-last"
				}
				t.Run(name, func(t *testing.T) {
					profile := base
					if modeFirst {
						profile = profile.WithReadMode(mode)
					}
					profile = profile.WithReadPaths(allowed, denied).WithWritePaths(writable).WithNoAccessPaths(denied)
					if !modeFirst {
						profile = profile.WithReadMode(mode)
					}
					rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					script := `cat "$1" && test ! -r "$3" && printf changed > "$4" && if test -r "$2"; then printf readable; else printf hidden; fi`
					result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
						Cmd: "/bin/sh", Args: []string{"-c", script, "test", allowed, ungranted, denied, writable},
					})
					want := "hosthidden"
					if mode == ReadModeHost {
						want = "hostreadable"
					}
					if err != nil || result.ExitCode != 0 || result.Stdout != want {
						t.Fatalf("builder order: %#v, %v, want %s", result, err, want)
					}
					data, err := os.ReadFile(writable)
					if err != nil || string(data) != "changed" {
						t.Fatalf("write grant lost: %q, %v", data, err)
					}
				})
			}
		}
	}
}

func TestReadModeNativeSymlinkCredentialAncestor(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	home, store := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	alias := filepath.Join(home, ".config")
	if err := os.Symlink(store, alias); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(store, "gcloud", "credentials.db")
	if err := os.MkdirAll(filepath.Dir(credential), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{credential: "credential", filepath.Join(store, "public"): "public"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logicalCredential := filepath.Join(alias, "gcloud", "credentials.db")
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, writeParent := range []bool{false, true} {
			name := string(mode) + "/read-parent"
			if writeParent {
				name = string(mode) + "/write-parent"
			}
			t.Run(name, func(t *testing.T) {
				profile := WorkspaceWriteProfile().WithReadMode(mode)
				if writeParent {
					profile = profile.WithWritePaths(alias)
				} else {
					profile = profile.WithReadPaths(alias)
				}
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				spec := codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{
					"-c", `cat "$1" && test ! -r "$2" && test ! -r "$3"`, "test",
					filepath.Join(alias, "public"), logicalCredential, credential,
				}}
				result, err := rt.RunProgram(ctx, ws, spec)
				if err != nil || result.ExitCode != 0 || result.Stdout != "public" {
					t.Fatalf("symlink parent protection: %#v, %v", result, err)
				}
				for _, grant := range []string{logicalCredential, credential} {
					permissions := WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{grant}})
					spec.Args = []string{"-c", `cat "$1" && cat "$2"`, "test", logicalCredential, credential}
					result, err := rt.RunProgram(permissions, ws, spec)
					if err != nil || result.ExitCode != 0 || result.Stdout != "credentialcredential" {
						t.Fatalf("exact grant via %s: %#v, %v", grant, result, err)
					}
				}
			})
		}
	}
}

func TestReadModeNativeParentAndAliasProtections(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := filepath.Join(home, ".ssh", "key")
	config := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{key, config} {
		if err := os.WriteFile(path, []byte("credential"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	homeAlias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, homeAlias); err != nil {
		t.Fatal(err)
	}
	for _, base := range []PermissionProfile{ReadOnlyProfile(), WorkspaceWriteProfile()} {
		for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
			t.Run(string(mode)+"/"+string(base.fileSystem.Rules[len(base.fileSystem.Rules)-1].Access), func(t *testing.T) {
				root := t.TempDir()
				rootAlias := filepath.Join(t.TempDir(), "root-alias")
				if err := os.Symlink(root, rootAlias); err != nil {
					t.Fatal(err)
				}
				for _, parentGrant := range []string{"read-parent", "write-parent", "root-read"} {
					t.Run(parentGrant, func(t *testing.T) {
						profile := base.WithReadMode(mode)
						if parentGrant == "write-parent" {
							profile = profile.WithWritePaths(root, rootAlias, homeAlias)
						} else {
							profile = profile.WithReadPaths(root, rootAlias, homeAlias)
							if parentGrant == "root-read" {
								profile = profile.WithReadPaths("/")
							}
						}
						rt := NewRuntime(WithWorkspaceRoot(root), WithPermissionProfile(profile))
						ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
						if err != nil {
							t.Fatal(err)
						}
						peer, err := rt.CreateWorkspace(ctx, "peer", codeexecutor.WorkspacePolicy{})
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(peer.Path, "work", "peer"), []byte("peer"), 0o644); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(ws.Path, "work", "current"), []byte("current"), 0o644); err != nil {
							t.Fatal(err)
						}
						aliasCurrent := filepath.Join(rootAlias, "sandbox", "current", "work", "current")
						aliasPeer := filepath.Join(rootAlias, "sandbox", "peer", "work", "peer")
						script := `test ! -r "$1" && test ! -r "$2" && test ! -r "$3" && test ! -r "$4" && cat "$5" && test ! -w "$6" && if test "$7" = read; then test ! -w "$5"; else test -w "$5"; fi`
						spec := codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{"-c", script, "test", key, filepath.Join(homeAlias, ".ssh", "key"), filepath.Join(peer.Path, "work", "peer"), aliasPeer, aliasCurrent, filepath.Join(rootAlias, "sandbox", "current", ".git"), string(base.fileSystem.Rules[len(base.fileSystem.Rules)-1].Access)}}
						result, err := rt.RunProgram(ctx, ws, spec)
						if err != nil || result.ExitCode != 0 || result.Stdout != "current" {
							t.Fatalf("parent protections: %#v, %v", result, err)
						}
						configAlias := filepath.Join(t.TempDir(), "config-alias")
						if err := os.Symlink(config, configAlias); err != nil {
							t.Fatal(err)
						}
						for _, allowed := range []string{config, configAlias} {
							grant := WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{allowed}})
							spec.Args = []string{"-c", `test ! -r "$1" && cat "$2" && cat "$3"`, "test", key, config, filepath.Join(homeAlias, ".ssh", "config")}
							result, err = rt.RunProgram(grant, ws, spec)
							if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "credentialcredential" {
								t.Fatalf("exact credential grant via %s: %#v, %v", allowed, result, err)
							}
						}
					})
				}
			})
		}
	}
}

func TestReadModeNativeEffectiveGrants(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		t.Run(string(mode), func(t *testing.T) {
			host := t.TempDir()
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
			ws, err := rt.CreateWorkspace(ctx, "effective", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			for _, root := range []string{host, filepath.Join(ws.Path, "work")} {
				readOnly := filepath.Join(root, "readonly")
				writable := filepath.Join(readOnly, "writable")
				if err := os.MkdirAll(writable, 0o755); err != nil {
					t.Fatal(err)
				}
				rt.profile = WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(writable, root).WithReadPaths(readOnly)
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", `test ! -w "$1" && printf ok > "$2/data" && cat "$2/data"`, "test", readOnly, writable},
				})
				if err != nil || result.ExitCode != 0 || result.Stdout != "ok" {
					t.Fatalf("effective grants for %s: %#v, %v", root, result, err)
				}
			}
		})
	}
}

func TestReadModeNativeWorkspaceGrantAliases(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, directory := range []bool{false, true} {
			for _, rootAlias := range []bool{false, true} {
				name := string(mode) + "/file"
				if directory {
					name = string(mode) + "/directory"
				}
				if rootAlias {
					name += "/root-alias"
				}
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					if rootAlias {
						alias := filepath.Join(t.TempDir(), "root-alias")
						if err := os.Symlink(root, alias); err != nil {
							t.Fatal(err)
						}
						root = alias
					}
					rt := NewRuntime(WithWorkspaceRoot(root))
					ws, err := rt.CreateWorkspace(ctx, "grant-aliases", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					readOnly := filepath.Join(ws.Path, "work", "readonly")
					target := readOnly
					if directory {
						if err := os.MkdirAll(filepath.Join(readOnly, "writable"), 0o755); err != nil {
							t.Fatal(err)
						}
						target = filepath.Join(readOnly, "data")
					}
					if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
						t.Fatal(err)
					}
					alias := filepath.Join(t.TempDir(), "read-alias")
					if err := os.Symlink(readOnly, alias); err != nil {
						t.Fatal(err)
					}
					aliasTarget := alias
					if directory {
						aliasTarget = filepath.Join(alias, "data")
					}
					profile := WorkspaceWriteProfile().WithReadMode(mode).WithReadPaths(alias)
					writable := filepath.Join(ws.Path, "work", "writable")
					if directory {
						writable = filepath.Join(readOnly, "writable", "data")
						writeAlias := filepath.Join(t.TempDir(), "write-alias")
						if err := os.Symlink(filepath.Dir(writable), writeAlias); err != nil {
							t.Fatal(err)
						}
						profile = profile.WithWritePaths(writeAlias)
					}
					rt.profile = profile
					rel, err := filepath.Rel(ws.Path, target)
					if err != nil {
						t.Fatal(err)
					}
					if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: rel, Content: []byte("forbidden")}}); !isKind(err, ErrPathDenied) {
						t.Fatalf("file API accepted a write beneath a read grant: %v", err)
					}
					script := `cat "$1" && cat "$2" || exit 1
if (printf forbidden > "$1") 2>/dev/null; then exit 2; fi
if (printf forbidden > "$2") 2>/dev/null; then exit 3; fi
printf changed > "$3" && cat "$3"`
					result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
						Cmd: "/bin/sh", Args: []string{"-c", script, "test", target, aliasTarget, writable},
					})
					if err != nil || result.ExitCode != 0 || result.Stdout != "originaloriginalchanged" {
						t.Fatalf("workspace alias grants: %#v, %v", result, err)
					}
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "original" {
						t.Fatalf("read-only workspace resource changed: %q, %v", data, err)
					}
				})
			}
		}
	}
}

func TestReadModeNativeMetadataDoesNotGrantReads(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	// A custom profile supplies runtime resources but only selected workspace
	// paths. Metadata protection must not create an independent read grant.
	var paths []string
	for _, path := range platformReadPaths() {
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}
	}
	profile := PermissionProfile{}.WithReadPaths(append(paths, "work")...).WithWritePaths(".git/config")
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
	ws, err := rt.CreateWorkspace(ctx, "metadata", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config", "hidden"} {
		if err := os.WriteFile(filepath.Join(ws.Path, ".git", name), []byte("metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "/bin/sh", Args: []string{"-c", `test ! -r "$1/.git/hidden" && test ! -w "$1/.git/config" && cat "$1/.git/config"`, "test", ws.Path}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "metadata" {
		t.Fatalf("metadata access: %#v, %v", result, err)
	}
}

func TestProtectedMetadataCanonicalAlias(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(ctx, "metadata-alias", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(ws.Path, "work", "metadata")
	if err := os.MkdirAll(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(metadata, "config")
	if err := os.WriteFile(file, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(metadata, filepath.Join(ws.Path, ".git")); err != nil {
		t.Skipf("symlink aliases unavailable: %v", err)
	}
	for _, path := range []string{".git/config", "work/metadata/config"} {
		if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: path, Content: []byte("changed")}}); !isKind(err, ErrPathDenied) {
			t.Fatalf("protected write via %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "original" {
		t.Fatalf("protected contents changed: %q, %v", data, err)
	}
}

func TestReadModeNativeDefaultRuntimeGrants(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	// Compare only tools within default runtime grants. Host PATH may contain
	// executables that granted mode intentionally cannot read or run.
	script := `
for c in /usr/bin/awk /usr/bin/perl /usr/bin/python3 /usr/bin/git; do
  if [ -x "$c" ] && "$c" --version >/dev/null 2>&1 </dev/null; then echo "$c=ok"
  elif [ -x "$c" ] && "$c" -W version >/dev/null 2>&1 </dev/null; then echo "$c=ok"
  else echo "$c=unavailable"; fi
done
tz=$(/bin/date +%Z) || exit 1
printf 'tz=%s\n' "$tz"
`
	for _, tc := range []struct {
		name    string
		profile PermissionProfile
	}{
		{name: "read_only", profile: ReadOnlyProfile()},
		{name: "workspace_write", profile: WorkspaceWriteProfile()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(mode ReadMode) string {
				rt := NewRuntime(
					WithWorkspaceRoot(t.TempDir()),
					WithPermissionProfile(tc.profile.WithReadMode(mode)),
					WithShellEnvironmentPolicy(ShellEnvironmentPolicy{Inherit: ShellEnvironmentPolicyInheritNone}),
				)
				ws, err := rt.CreateWorkspace(ctx, "runtime", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd:  "/bin/sh",
					Args: []string{"-c", script},
					Env:  map[string]string{"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL": "C"},
				})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("%s runtime probe: %#v, %v", mode, result, err)
				}
				return result.Stdout
			}
			if host, granted := run(ReadModeHost), run(ReadModeGranted); host != granted {
				t.Fatalf("default runtime grants differ from host:\nhost:\n%s\ngranted:\n%s", host, granted)
			}
		})
	}
}
