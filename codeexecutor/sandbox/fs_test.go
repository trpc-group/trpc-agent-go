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

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestHostStagingAncestorSymlinkGrants(t *testing.T) {
	for _, grant := range []string{
		"ungranted",
		"workspace-parent",
		"relative-parent",
		"absolute-resource",
		"absolute-alias",
		"relative-alias",
		"temporary-resource",
		"write-resource",
		"workspace-resource",
		"workspace-root-alias",
		"special-root-ungranted",
		"special-root-explicit",
		"host-fallback",
		"disabled",
	} {
		for _, directory := range []bool{false, true} {
			for _, input := range []bool{false, true} {
				name := grant + "/file/directory-api"
				if directory {
					name = grant + "/directory/directory-api"
				}
				if input {
					name += "/host-input"
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					root := t.TempDir()
					if grant == "workspace-root-alias" {
						alias := filepath.Join(t.TempDir(), "workspace-root")
						if err := os.Symlink(root, alias); err != nil {
							t.Skipf("symlink aliases unavailable: %v", err)
						}
						root = alias
					}
					rt := NewRuntime(WithWorkspaceRoot(root))
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					parent := t.TempDir()
					if grant == "workspace-resource" || grant == "workspace-root-alias" {
						parent = filepath.Join(ws.Path, "out")
					}
					real := filepath.Join(parent, "source")
					if err := os.MkdirAll(real, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(real, "data"), []byte("staged"), 0o600); err != nil {
						t.Fatal(err)
					}
					alias := filepath.Join(ws.Path, "work", "alias")
					to := "work/copied"
					if grant == "special-root-ungranted" || grant == "special-root-explicit" {
						alias = filepath.Join(ws.Path, "work")
						if err := os.Remove(alias); err != nil {
							t.Fatal(err)
						}
						to = "out/copied"
					}
					if err := os.Symlink(parent, alias); err != nil {
						t.Skipf("symlink aliases unavailable: %v", err)
					}
					source := filepath.Join(alias, "source")
					if !directory {
						source = filepath.Join(source, "data")
					}
					profile := WorkspaceWriteProfile()
					allowed := true
					switch grant {
					case "ungranted", "special-root-ungranted":
						allowed = false
					case "workspace-parent":
						profile = profile.WithReadPaths(ws.Path)
						allowed = false
					case "relative-parent":
						profile = profile.WithReadPaths("work")
						allowed = false
					case "absolute-resource", "special-root-explicit":
						profile = profile.WithReadPaths(real)
					case "absolute-alias":
						profile = profile.WithReadPaths(filepath.Join(alias, "source"))
					case "relative-alias":
						profile = profile.WithReadPaths("work/alias/source")
					case "temporary-resource":
						ctx = WithAdditionalPermissions(ctx, AdditionalPermissions{ReadPaths: []string{real}})
					case "write-resource":
						profile = profile.WithWritePaths(real)
					case "host-fallback":
						profile = profile.WithReadMode(ReadModeHost)
					case "disabled":
						profile = DangerFullAccessProfile()
					}
					rt.profile = profile
					if input {
						err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{From: inputSchemeHost + source, To: to}})
					} else {
						err = rt.StageDirectory(ctx, ws, source, to, codeexecutor.StageOptions{})
					}
					if !allowed {
						if !isKind(err, ErrPathDenied) {
							t.Fatalf("ungranted ancestor-link source was accepted: %v", err)
						}
						if _, err := os.Stat(filepath.Join(ws.Path, filepath.FromSlash(to))); !os.IsNotExist(err) {
							t.Fatalf("ungranted source was copied: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("authorized source rejected: %v", err)
					}
					copied := filepath.Join(ws.Path, filepath.FromSlash(to))
					if directory {
						copied = filepath.Join(copied, "data")
					}
					data, err := os.ReadFile(copied)
					if err != nil || string(data) != "staged" {
						t.Fatalf("copied content = %q, %v", data, err)
					}
				})
			}
		}
	}
}

func TestHostStagingAncestorSymlinkDenials(t *testing.T) {
	for _, mode := range []ReadMode{ReadModeGranted, ReadModeHost} {
		for _, denial := range []string{"lexical-glob", "lexical-path", "canonical-path", "canonical-glob"} {
			for _, input := range []bool{false, true} {
				name := string(mode) + "/" + denial + "/directory-api"
				if input {
					name += "/host-input"
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
					ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
					if err != nil {
						t.Fatal(err)
					}
					parent := t.TempDir()
					if denial == "canonical-glob" {
						parent = filepath.Join(ws.Path, "out")
					}
					real := filepath.Join(parent, "source", "data")
					if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(real, []byte("denied"), 0o600); err != nil {
						t.Fatal(err)
					}
					alias := filepath.Join(ws.Path, "work", "alias")
					if err := os.Symlink(parent, alias); err != nil {
						t.Skipf("symlink aliases unavailable: %v", err)
					}
					source := filepath.Join(alias, "source", "data")
					profile := WorkspaceWriteProfile().WithReadMode(mode).WithReadPaths(parent)
					switch denial {
					case "lexical-glob":
						profile = profile.WithNoAccessGlobs("work/alias/source/**")
					case "lexical-path":
						profile = profile.WithNoAccessPaths(source)
					case "canonical-path":
						profile = profile.WithNoAccessPaths(real)
					case "canonical-glob":
						profile = profile.WithNoAccessGlobs("out/source/**")
					}
					rt.profile = profile
					if input {
						err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{From: inputSchemeHost + source, To: "work/copied"}})
					} else {
						err = rt.StageDirectory(ctx, ws, source, "work/copied", codeexecutor.StageOptions{})
					}
					if !isKind(err, ErrPathDenied) {
						t.Fatalf("denied source accepted through ancestor link: %v", err)
					}
					if _, err := os.Stat(filepath.Join(ws.Path, "work", "copied")); !os.IsNotExist(err) {
						t.Fatalf("denied source was copied: %v", err)
					}
				})
			}
		}
	}
}

func TestRelativeGrantsCannotAuthorizeHostPathsOutsideWorkspace(t *testing.T) {
	ctx := context.Background()
	for _, grant := range []string{"../..", "../../outside"} {
		for _, access := range []fileSystemAccess{accessRead, accessWrite} {
			for _, temporary := range []bool{false, true} {
				for _, input := range []bool{false, true} {
					name := grant + "/" + string(access) + "/profile/directory"
					if temporary {
						name = grant + "/" + string(access) + "/temporary/directory"
					}
					if input {
						name += "/host-input"
					}
					t.Run(name, func(t *testing.T) {
						root := t.TempDir()
						profile := WorkspaceWriteProfile()
						stageCtx := ctx
						if temporary {
							add := AdditionalPermissions{}
							if access == accessWrite {
								add.WritePaths = []string{grant}
							} else {
								add.ReadPaths = []string{grant}
							}
							stageCtx = WithAdditionalPermissions(ctx, add)
						} else if access == accessWrite {
							profile = profile.WithWritePaths(grant)
						} else {
							profile = profile.WithReadPaths(grant)
						}
						rt := NewRuntime(WithWorkspaceRoot(root), WithPermissionProfile(profile))
						ws, err := rt.CreateWorkspace(ctx, "current", codeexecutor.WorkspacePolicy{})
						if err != nil {
							t.Fatal(err)
						}
						source := filepath.Join(root, "outside", "data")
						if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(source, []byte("outside"), 0o600); err != nil {
							t.Fatal(err)
						}
						if input {
							err = rt.StageInputs(stageCtx, ws, []codeexecutor.InputSpec{{
								From: inputSchemeHost + source,
								To:   "work/copied",
							}})
						} else {
							err = rt.StageDirectory(stageCtx, ws, source, "work/copied", codeexecutor.StageOptions{})
						}
						if !isKind(err, ErrPathDenied) {
							t.Fatalf("escaping relative grant authorized host read: %v", err)
						}
						if _, err := os.Stat(filepath.Join(ws.Path, "work", "copied")); !os.IsNotExist(err) {
							t.Fatalf("host data copied outside workspace grant: %v", err)
						}
					})
				}
			}
		}
	}
}
