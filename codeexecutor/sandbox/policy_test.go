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

func TestNoAccessGlobCollectDenied(t *testing.T) {
	profile := WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, "work", "app.env"),
		[]byte("TOKEN=secret"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Collect(context.Background(), ws, []string{"work/*.env"})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected no-access glob to block Collect, got %v", err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/app.env",
		Content: []byte("TOKEN=new"),
	}})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected no-access glob to block PutFiles, got %v", err)
	}
}

func TestAccessNonePathDeniesReadAndWrite(t *testing.T) {
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/secret.txt")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "none-path", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, "work", "secret.txt"),
		[]byte("secret"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/secret.txt"}); !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected AccessNone to deny read, got %v", err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/secret.txt",
		Content: []byte("new"),
	}})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected AccessNone to deny write, got %v", err)
	}
}

func TestMoreSpecificReadRuleOverridesWorkspaceWrite(t *testing.T) {
	profile := WorkspaceWriteProfile().WithReadPaths("work/readonly")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "specific-read", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ws.Path, "work", "readonly")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"work/readonly/note.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "ok" {
		t.Fatalf("read-only subtree collect = %#v", files)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/readonly/note.txt",
		Content: []byte("new"),
	}})
	if !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected more specific read rule to deny write, got %v", err)
	}
}

func TestEqualSpecificityAccessPrecedence(t *testing.T) {
	profile := WorkspaceWriteProfile()
	profile.FileSystem.Rules = append(profile.FileSystem.Rules,
		FileSystemRule{Kind: RulePath, Access: AccessRead, Path: "work/tie.txt"},
		FileSystemRule{Kind: RulePath, Access: AccessWrite, Path: "work/tie.txt"},
		FileSystemRule{Kind: RulePath, Access: AccessNone, Path: "work/tie.txt"},
	)
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "tie", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "tie.txt"), []byte("tie"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := rt.decidePath(normalizeProfile(profile), ws, "work/tie.txt")
	if err != nil {
		t.Fatal(err)
	}
	if d.access != AccessNone {
		t.Fatalf("equal-specificity access = %s, want %s", d.access, AccessNone)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/tie.txt"}); !IsKind(err, ErrPathDenied) {
		t.Fatalf("expected equal-specificity AccessNone to deny read, got %v", err)
	}
}

func TestRuleGlobOnlySupportsAccessNone(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "glob-shape", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []FileSystemAccess{AccessRead, AccessWrite} {
		profile := WorkspaceWriteProfile()
		profile.FileSystem.Rules = append(profile.FileSystem.Rules, FileSystemRule{
			Kind: RuleGlob, Access: access, Glob: "work/*.env",
		})
		err := rt.checkRead(profile, ws, codeexecutor.DirWork)
		if !IsKind(err, ErrPolicyViolation) {
			t.Fatalf("glob access %s: expected ErrPolicyViolation, got %v", access, err)
		}
	}
}

func TestAccessNoneMaskCollectionAcceptsPathGlobAndSpecial(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "none-supported", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/app.env",
		Content: []byte("TOKEN=secret"),
	}}); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
	profile = profile.WithNoAccessPaths("work/app.env")
	profile.FileSystem.Rules = append(profile.FileSystem.Rules, FileSystemRule{
		Kind: RuleSpecial, Access: AccessNone, Special: SpecialOut,
	})
	matches, err := rt.deniedReadMatches(profile, ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(ws.Path, "work", "app.env"),
		filepath.Join(ws.Path, codeexecutor.DirOut),
	} {
		if !containsPath(matches, want) {
			t.Fatalf("AccessNone matches = %#v, missing %s", matches, want)
		}
	}
}

func TestPermissionProfileBuildersDoNotAliasRules(t *testing.T) {
	base := WorkspaceWriteProfile()
	withRead := base.WithReadPaths("work/read")
	withNone := base.WithNoAccessPaths("work/none")
	if containsRule(withRead, AccessNone, "work/none") {
		t.Fatalf("WithNoAccessPaths mutated sibling profile rules: %#v", withRead.FileSystem.Rules)
	}
	if containsRule(withNone, AccessRead, "work/read") {
		t.Fatalf("WithReadPaths mutated sibling profile rules: %#v", withNone.FileSystem.Rules)
	}
	if containsRule(base, AccessRead, "work/read") || containsRule(base, AccessNone, "work/none") {
		t.Fatalf("builder mutated base profile rules: %#v", base.FileSystem.Rules)
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

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func containsRule(profile PermissionProfile, access FileSystemAccess, path string) bool {
	for _, rule := range profile.FileSystem.Rules {
		if rule.Kind == RulePath && rule.Access == access && rule.Path == path {
			return true
		}
	}
	return false
}
