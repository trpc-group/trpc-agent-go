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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestPermissionProfileEnforcement(t *testing.T) {
	if got := WorkspaceWriteProfile().Enforcement(); got != EnforcementManaged {
		t.Fatalf("workspace_write enforcement = %s", got)
	}
	if got := ReadOnlyProfile().Enforcement(); got != EnforcementManaged {
		t.Fatalf("read_only enforcement = %s", got)
	}
	if got := DangerFullAccessProfile().Enforcement(); got != EnforcementDisabled {
		t.Fatalf("danger_full_access enforcement = %s", got)
	}
	if got := ExternalSandboxProfile(NetworkPolicy{}).Enforcement(); got != EnforcementExternal {
		t.Fatalf("external_sandbox enforcement = %s", got)
	}
}

func TestEnvironmentPolicyCoreDoesNotInheritLLMKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-secret")
	rt := NewRuntime(WithPermissionProfile(DangerFullAccessProfile()))
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") {
			t.Fatalf("core environment inherited OPENAI_API_KEY")
		}
	}
	if !hasEnv(env, "HOME="+filepath.Join(ws.Path, "home")) {
		t.Fatalf("HOME did not point at sandbox home: %v", env)
	}
}

func TestEnvironmentPolicyIncludeExcludeSet(t *testing.T) {
	t.Setenv("SANDBOX_KEEP", "yes")
	t.Setenv("SANDBOX_DROP", "no")
	rt := NewRuntime(
		WithPermissionProfile(DangerFullAccessProfile()),
		WithEnvironmentPolicy(EnvironmentPolicy{
			Inherit:     EnvInheritAll,
			IncludeOnly: []string{"SANDBOX_*"},
			Exclude:     []string{"*_DROP"},
			Set:         map[string]string{"SANDBOX_SET": "ok"},
		}),
	)
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if !hasEnv(env, "SANDBOX_KEEP=yes") {
		t.Fatalf("expected SANDBOX_KEEP in env: %v", env)
	}
	if hasEnvPrefix(env, "SANDBOX_DROP=") {
		t.Fatalf("expected SANDBOX_DROP excluded: %v", env)
	}
	if !hasEnv(env, "SANDBOX_SET=ok") {
		t.Fatalf("expected SANDBOX_SET override: %v", env)
	}
}

func TestProtectedMetadataWriteDenied(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    ".git/config",
		Content: []byte("bad"),
	}})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected ErrPathDenied, got %v", err)
	}
}

func TestDenyReadGlobCollectDenied(t *testing.T) {
	profile := WorkspaceWriteProfile().WithDenyReadGlobs("work/*.env")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/app.env",
		Content: []byte("TOKEN=secret"),
	}}); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Collect(context.Background(), ws, []string{"work/*.env"})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected deny-read glob to block Collect, got %v", err)
	}
}

func TestSessionPersistenceAndIsolation(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ctx := context.Background()
	s1a, err := rt.CreateWorkspace(ctx, "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(ctx, s1a, []codeexecutor.PutFile{{
		Path:    "work/marker.txt",
		Content: []byte("s1"),
	}}); err != nil {
		t.Fatal(err)
	}
	s1b, err := rt.CreateWorkspace(ctx, "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if s1a.Path != s1b.Path {
		t.Fatalf("same session got different paths: %s vs %s", s1a.Path, s1b.Path)
	}
	files, err := rt.Collect(ctx, s1b, []string{"work/marker.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "s1" {
		t.Fatalf("same session did not see marker: %#v", files)
	}
	s2, err := rt.CreateWorkspace(ctx, "s2", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	files, err = rt.Collect(ctx, s2, []string{"work/marker.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("different session saw marker: %#v", files)
	}
}

func TestWorkspacePathUsesAppUserSessionShape(t *testing.T) {
	root := t.TempDir()
	path, id := workspacePathForID(root, "app/user/session")
	want := filepath.Join(root, "sandbox", "app", "user", "session")
	if path != want {
		t.Fatalf("workspace path = %s, want %s", path, want)
	}
	if id != "app_user_session" {
		t.Fatalf("workspace id = %s", id)
	}
}

func TestManifestMaterializesInitialFilesAndEnv(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithManifest(Manifest{
			Files: []ManifestFile{{
				Path:    "work/input.txt",
				Content: []byte("seed"),
				Mode:    0o640,
			}},
			Environment: map[string]string{"SANDBOX_MANIFEST": "yes"},
		}),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"work/input.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "seed" {
		t.Fatalf("manifest file missing: %#v", files)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if !hasEnv(env, "SANDBOX_MANIFEST=yes") {
		t.Fatalf("manifest env missing: %v", env)
	}
}

func TestAdditionalPermissionsAreScopedToContext(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	hostDir := t.TempDir()
	hostFile := filepath.Join(hostDir, "input.txt")
	if err := os.WriteFile(hostFile, []byte("granted"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = rt.StageDirectory(ctx, ws, hostFile, "work/no-grant.txt", codeexecutor.StageOptions{})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected read denial without grant, got %v", err)
	}
	grantCtx := WithAdditionalPermissions(ctx, AdditionalPermissions{
		ReadPaths: []string{hostFile},
	})
	if err := rt.StageDirectory(grantCtx, ws, hostFile, "work/granted.txt", codeexecutor.StageOptions{}); err != nil {
		t.Fatal(err)
	}
	err = rt.StageDirectory(ctx, ws, hostFile, "work/no-grant-again.txt", codeexecutor.StageOptions{})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected grant to be scoped to one context, got %v", err)
	}
}

func TestRunProgramOutputCapAndTimeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
		WithOutputMaxBytes(32),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "printf '%*s' 200 x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "[truncated]") {
		t.Fatalf("expected truncated marker, got %q", res.Stdout)
	}
	_, err = rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", "sleep 5"},
		Timeout: 10 * time.Millisecond,
	})
	if !IsKind(err, ErrTimeout) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func hasEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func hasEnvPrefix(env []string, prefix string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
