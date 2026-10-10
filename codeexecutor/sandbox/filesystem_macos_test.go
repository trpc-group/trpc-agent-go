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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func macosCredentialFixture(t *testing.T, layout string) (home, credential string) {
	t.Helper()
	home = t.TempDir()
	credential = filepath.Join(home, ".ssh")
	switch layout {
	case "directory":
		if err := os.Mkdir(credential, 0o700); err != nil {
			t.Fatal(err)
		}
	case "credential-link":
		if err := os.Symlink(t.TempDir(), credential); err != nil {
			t.Fatal(err)
		}
	case "home-link":
		physicalHome := home
		home = filepath.Join(t.TempDir(), "home")
		if err := os.Symlink(physicalHome, home); err != nil {
			t.Fatal(err)
		}
		credential = filepath.Join(home, ".ssh")
		if err := os.Symlink(t.TempDir(), credential); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown layout %q", layout)
	}
	t.Setenv("HOME", home)
	for _, name := range []string{"config", "key"} {
		if err := os.WriteFile(filepath.Join(credential, name), []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, credential
}

func TestMacOSNativeCredentialEntryProtection(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, layout := range []string{"directory", "credential-link", "home-link"} {
			for _, operation := range []string{"read", "write", "unlink", "rename", "replacement"} {
				t.Run(string(mode)+"/"+layout+"/"+operation, func(t *testing.T) {
					home, credential := macosCredentialFixture(t, layout)
					canonical := policyCanonicalPath(credential)
					rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(
						WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(home),
					))
					t.Cleanup(func() { _ = rt.Close() })
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					config := filepath.Join(credential, "config")
					moved := filepath.Join(home, "moved-credential")
					var script string
					switch operation {
					case "read":
						script = `cat "$1/config"`
					case "write":
						script = `printf changed > "$1/config"`
					case "unlink":
						script = `rm -r "$1"`
						if layout != "directory" {
							script = `/usr/bin/perl -e 'unlink($ARGV[0]) or die "$!\n"' "$1"`
						}
					case "rename":
						script = `/usr/bin/perl -e 'rename($ARGV[0], $ARGV[1]) or die "$!\n"' "$1" "$2"`
					case "replacement":
						script = `rm -r "$1" && mkdir "$1" && printf changed > "$1/config"`
						if layout != "directory" {
							script = `/usr/bin/perl -e 'unlink($ARGV[0]) or die "$!\n"' "$1" && mkdir "$1" && printf changed > "$1/config"`
						}
					}
					result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
						Cmd: "/bin/sh", Args: []string{"-c", script, "test", credential, moved},
					})
					if err != nil || result.ExitCode == 0 {
						t.Fatalf("protected credential %s: %#v, %v", operation, result, err)
					}
					if operation == "read" && result.Stdout != "" {
						t.Fatalf("protected content exposed: %q", result.Stdout)
					}
					if got := policyCanonicalPath(credential); got != canonical {
						t.Fatalf("credential entry changed: %q, want %q", got, canonical)
					}
					for _, path := range []string{config, filepath.Join(canonical, "config"), filepath.Join(credential, "key")} {
						data, err := os.ReadFile(path)
						if err != nil || string(data) != "original" {
							t.Fatalf("credential changed at %s: %q, %v", path, data, err)
						}
					}
					if _, err := os.Lstat(moved); !os.IsNotExist(err) {
						t.Fatalf("credential moved: %v", err)
					}
				})
			}
		}
	}
}

func TestMacOSNativeCredentialGrantExceptions(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, layout := range []string{"directory", "credential-link", "home-link"} {
			for _, grantKind := range []string{"read-directory", "read-file", "write-directory", "write-file"} {
				for _, canonicalGrant := range []bool{false, true} {
					name := string(mode) + "/" + layout + "/" + grantKind + "/logical"
					if canonicalGrant {
						name = string(mode) + "/" + layout + "/" + grantKind + "/canonical"
					}
					t.Run(name, func(t *testing.T) {
						home, credential := macosCredentialFixture(t, layout)
						grant := credential
						fileGrant := grantKind == "read-file" || grantKind == "write-file"
						writeGrant := grantKind == "write-directory" || grantKind == "write-file"
						if fileGrant {
							grant = filepath.Join(grant, "config")
						}
						if canonicalGrant {
							grant = policyCanonicalPath(grant)
						}
						profile := WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(home)
						if writeGrant {
							profile = profile.WithWritePaths(grant)
						} else {
							profile = profile.WithReadPaths(grant)
						}
						rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
						t.Cleanup(func() { _ = rt.Close() })
						ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
						if err != nil {
							t.Fatal(err)
						}
						config, key := filepath.Join(credential, "config"), filepath.Join(credential, "key")
						result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
							Cmd: "/bin/cat", Args: []string{config},
						})
						if err != nil || result.ExitCode != 0 || result.Stdout != "original" {
							t.Fatalf("exact grant read: %#v, %v", result, err)
						}
						result, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
							Cmd: "/bin/cat", Args: []string{key},
						})
						if err != nil || (result.ExitCode == 0) == fileGrant {
							t.Fatalf("sibling credential read: %#v, %v", result, err)
						}
						result, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
							Cmd: "/bin/sh", Args: []string{"-c", `printf changed > "$1"`, "test", config},
						})
						if err != nil || (result.ExitCode == 0) != writeGrant {
							t.Fatalf("exact grant write: %#v, %v", result, err)
						}
						want := "original"
						if writeGrant {
							want = "changed"
						}
						data, err := os.ReadFile(config)
						if err != nil || string(data) != want {
							t.Fatalf("credential content: %q, %v, want %q", data, err, want)
						}
					})
				}
			}
		}
	}
}

func TestMacOSNativeCredentialGrantRetainsNoAccess(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, canonicalGrant := range []bool{false, true} {
			name := string(mode) + "/logical"
			if canonicalGrant {
				name = string(mode) + "/canonical"
			}
			t.Run(name, func(t *testing.T) {
				home, credential := macosCredentialFixture(t, "credential-link")
				grant := credential
				if canonicalGrant {
					grant = policyCanonicalPath(grant)
				}
				config := filepath.Join(credential, "config")
				profile := WorkspaceWriteProfile().WithReadMode(mode).
					WithWritePaths(home, grant).WithNoAccessPaths(config)
				rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(profile))
				t.Cleanup(func() { _ = rt.Close() })
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				for _, script := range []string{`cat "$1"`, `printf changed > "$1"`} {
					result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
						Cmd: "/bin/sh", Args: []string{"-c", script, "test", config},
					})
					if err != nil || result.ExitCode == 0 || result.Stdout != "" {
						t.Fatalf("no-access credential reopened: %#v, %v", result, err)
					}
				}
				data, err := os.ReadFile(config)
				if err != nil || string(data) != "original" {
					t.Fatalf("denied credential changed: %q, %v", data, err)
				}
			})
		}
	}
}

func TestMacOSNativeProtectedAncestorRename(t *testing.T) {
	requireReadModeBackend(t)
	ctx := context.Background()
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, protection := range []string{"credential", "credential-home-link", "session", "no-access", "metadata", "read-only"} {
			t.Run(string(mode)+"/"+protection, func(t *testing.T) {
				parent := t.TempDir()
				home := filepath.Join(parent, "home")
				if err := os.Mkdir(home, 0o700); err != nil {
					t.Fatal(err)
				}
				if protection == "credential-home-link" {
					physical := filepath.Join(parent, "home-store")
					if err := os.Rename(home, physical); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(physical, home); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("HOME", home)
				rt := NewRuntime(WithWorkspaceRoot(filepath.Join(parent, "runtime")))
				t.Cleanup(func() { _ = rt.Close() })
				ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
				if err != nil {
					t.Fatal(err)
				}
				profile := WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(parent)
				ancestor := filepath.Join(parent, "data")
				protected := filepath.Join(ancestor, "private", "secret")
				moved := filepath.Join(parent, "moved")
				switch protection {
				case "credential", "credential-home-link":
					ancestor = home
					protected = filepath.Join(home, ".ssh", "config")
				case "session":
					peer, err := rt.CreateWorkspace(ctx, "peer", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					ancestor = rt.root
					protected = filepath.Join(peer.Path, "work", "secret")
				case "no-access":
					profile = profile.WithNoAccessPaths(protected)
				case "metadata":
					ancestor = filepath.Join(ws.Path, "work")
					metadata := filepath.Join(ancestor, "metadata")
					protected = filepath.Join(metadata, "config")
					if err := os.Symlink(metadata, filepath.Join(ws.Path, ".git")); err != nil {
						t.Fatal(err)
					}
					moved = filepath.Join(ws.Path, "out", "moved")
				case "read-only":
					profile = profile.WithReadPaths(filepath.Dir(protected))
				}
				if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(protected, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
				rt.profile = profile
				// Other entries beneath the writable ancestor remain writable.
				script := `printf ok > "$1/sibling" && rm "$1/sibling" || exit 2
/usr/bin/perl -e 'rename($ARGV[0], $ARGV[1]) or die "$!\n"' "$1" "$2"`
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", script, "test", ancestor, moved},
				})
				if err != nil || result.ExitCode == 0 || result.ExitCode == 2 {
					t.Fatalf("protected ancestor rename: %#v, %v", result, err)
				}
				data, err := os.ReadFile(protected)
				if err != nil || string(data) != "original" {
					t.Fatalf("protected resource moved: %q, %v", data, err)
				}
				if _, err := os.Lstat(moved); !os.IsNotExist(err) {
					t.Fatalf("ancestor was renamed: %v", err)
				}
			})
		}
	}
}

func TestMacOSNativeCredentialCreatedAfterStart(t *testing.T) {
	requireReadModeBackend(t)
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		t.Run(string(mode), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			rt := NewRuntime(WithWorkspaceRoot(t.TempDir()), WithPermissionProfile(
				WorkspaceWriteProfile().WithReadMode(mode).WithWritePaths(home),
			))
			t.Cleanup(func() { _ = rt.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
			if err != nil {
				t.Fatal(err)
			}
			credential := filepath.Join(home, ".ssh", "config")
			ready, proceed := filepath.Join(ws.Path, "work", "ready"), filepath.Join(ws.Path, "work", "proceed")
			type outcome struct {
				result codeexecutor.RunResult
				err    error
			}
			outcomes := make(chan outcome, 1)
			go func() {
				result, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
					Cmd: "/bin/sh", Args: []string{"-c", `printf ready > "$1"; while [ ! -e "$2" ]; do /bin/sleep 0.01; done; cat "$3"`, "test", ready, proceed, credential},
				})
				outcomes <- outcome{result: result, err: err}
			}()
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				select {
				case done := <-outcomes:
					t.Fatalf("program exited before ready: %#v, %v", done.result, done.err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := os.MkdirAll(filepath.Dir(credential), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credential, []byte("new credential"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(proceed, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			done := <-outcomes
			if done.err != nil || done.result.ExitCode == 0 || done.result.Stdout != "" {
				t.Fatalf("new credential exposed: %#v, %v", done.result, done.err)
			}
		})
	}
}
