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
	if got := WorkspaceWriteProfile().enforcement(); got != enforcementManaged {
		t.Fatalf("workspace_write enforcement = %s", got)
	}
	if got := ReadOnlyProfile().enforcement(); got != enforcementManaged {
		t.Fatalf("read_only enforcement = %s", got)
	}
	if got := DangerFullAccessProfile().enforcement(); got != enforcementDisabled {
		t.Fatalf("danger_full_access enforcement = %s", got)
	}
	if got := ExternalSandboxProfile(NetworkPolicy{}).enforcement(); got != enforcementExternal {
		t.Fatalf("external_sandbox enforcement = %s", got)
	}
}

func TestRuntimeDefaultProfileIsWorkspaceWrite(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if !containsSpecialRule(rt.profile, accessWrite, specialWork) {
		t.Fatalf("default runtime profile does not grant work writes: %#v", rt.profile.fileSystem.Rules)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "default-profile", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/ok.txt",
		Content: []byte("ok"),
	}}); err != nil {
		t.Fatalf("default workspace-write profile rejected work write: %v", err)
	}
}

func TestWithPermissionProfileKeepsCompleteManagedProfile(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(PermissionProfile{
			typ:     profileManaged,
			network: NetworkPolicy{Mode: NetworkEnabled},
		}),
	)
	if rt.profile.network.Mode != NetworkEnabled {
		t.Fatalf("network mode = %s, want %s", rt.profile.network.Mode, NetworkEnabled)
	}
	if len(rt.profile.fileSystem.Rules) != 0 {
		t.Fatalf("empty managed profile was expanded to rules: %#v", rt.profile.fileSystem.Rules)
	}
	if len(rt.profile.fileSystem.ProtectedMetadata) == 0 {
		t.Fatalf("protected metadata defaults were not populated")
	}
	if !rt.Describe().NetworkAllowed {
		t.Fatalf("Describe did not report enabled network")
	}
	ws, err := rt.CreateWorkspace(context.Background(), "strict-profile", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/blocked.txt",
		Content: []byte("blocked"),
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("empty managed filesystem rules should be restrictive, got %v", err)
	}
}

func TestWorkspaceWriteProfileCanSetNetwork(t *testing.T) {
	profile := WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	if profile.network.Mode != NetworkEnabled {
		t.Fatalf("network mode = %s, want %s", profile.network.Mode, NetworkEnabled)
	}
	if !containsSpecialRule(profile, accessWrite, specialWork) {
		t.Fatalf("workspace-write network profile missing work write grant: %#v", profile.fileSystem.Rules)
	}

	profile = WorkspaceWriteProfile().
		WithMacOSWeakerNetworkIsolation().
		WithMacOSUnixSocketPaths("/tmp/trpc-agent.sock")
	if !profile.macOS.allowSystemTrustServices ||
		len(profile.macOS.unixSocketPaths) != 1 ||
		profile.macOS.unixSocketPaths[0] != "/tmp/trpc-agent.sock" {
		t.Fatalf("macOS network extensions = %#v, want trust services and unix socket", profile.macOS)
	}

	profile = WorkspaceWriteProfile().
		WithMacOSWeakerNetworkIsolation().
		WithMacOSUnixSocketPaths("/tmp/trpc-agent.sock").
		WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	if profile.network.Mode != NetworkEnabled ||
		!profile.macOS.allowSystemTrustServices ||
		len(profile.macOS.unixSocketPaths) != 1 {
		t.Fatalf("network/macos extension order changed profile: network=%#v macOS=%#v", profile.network, profile.macOS)
	}
}

func TestShellEnvironmentPolicyDefaultAllInheritsHostEnv(t *testing.T) {
	t.Setenv("TRPC_SANDBOX_DEFAULT_ALL_VISIBLE", "yes")
	rt := NewRuntime(WithPermissionProfile(DangerFullAccessProfile()))
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if !hasEnv(env, "TRPC_SANDBOX_DEFAULT_ALL_VISIBLE=yes") {
		t.Fatalf("default All policy did not inherit host env: %v", env)
	}
	if !hasEnv(env, "HOME="+filepath.Join(ws.Path, "home")) {
		t.Fatalf("HOME did not point at sandbox home: %v", env)
	}
}

func TestShellEnvironmentPolicyCoreDoesNotInheritLLMKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-secret")
	rt := NewRuntime(
		WithPermissionProfile(DangerFullAccessProfile()),
		WithShellEnvironmentPolicy(ShellEnvironmentPolicy{
			Inherit: ShellEnvironmentPolicyInheritCore,
		}),
	)
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if hasEnvPrefix(env, "OPENAI_API_KEY=") {
		t.Fatalf("core environment inherited OPENAI_API_KEY")
	}
	if !hasEnv(env, "HOME="+filepath.Join(ws.Path, "home")) {
		t.Fatalf("HOME did not point at sandbox home: %v", env)
	}
}

func TestShellEnvironmentPolicyIncludeOnlyFiltersFinalCallerEnv(t *testing.T) {
	t.Setenv("SANDBOX_KEEP", "yes")
	t.Setenv("SANDBOX_DROP", "no")
	rt := NewRuntime(
		WithPermissionProfile(DangerFullAccessProfile()),
		WithShellEnvironmentPolicy(ShellEnvironmentPolicy{
			Inherit:     ShellEnvironmentPolicyInheritAll,
			IncludeOnly: []string{"SANDBOX_*"},
			Exclude:     []string{"*_DROP"},
			Set: map[string]string{
				"SANDBOX_SET":          "ok",
				"OTHER_SET_FILTERED":   "no",
				"SANDBOX_DROP_SET":     "no",
				"SANDBOX_CASE_MATCHED": "ok",
			},
		}),
	)
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{
		Env: map[string]string{
			"SANDBOX_RUN":        "ok",
			"OTHER_RUN_FILTERED": "no",
		},
	})
	if !hasEnv(env, "SANDBOX_KEEP=yes") {
		t.Fatalf("expected SANDBOX_KEEP in env: %v", env)
	}
	if hasEnvPrefix(env, "SANDBOX_DROP=") {
		t.Fatalf("expected SANDBOX_DROP excluded: %v", env)
	}
	if !hasEnv(env, "SANDBOX_SET=ok") {
		t.Fatalf("expected SANDBOX_SET override: %v", env)
	}
	if !hasEnv(env, "SANDBOX_RUN=ok") {
		t.Fatalf("expected matching per-run env in env: %v", env)
	}
	if hasEnvPrefix(env, "OTHER_SET_FILTERED=") || hasEnvPrefix(env, "OTHER_RUN_FILTERED=") {
		t.Fatalf("IncludeOnly should filter Set and per-run env: %v", env)
	}
	if !hasEnv(env, "HOME="+filepath.Join(ws.Path, "home")) {
		t.Fatalf("runtime HOME should be injected after IncludeOnly: %v", env)
	}
}

func TestShellEnvironmentPolicyExcludeBeforeSetAndCaseInsensitivePatterns(t *testing.T) {
	t.Setenv("SANDBOX_RESTORE", "host")
	t.Setenv("SANDBOX_SECRET_TOKEN", "secret")
	rt := NewRuntime(
		WithPermissionProfile(DangerFullAccessProfile()),
		WithShellEnvironmentPolicy(ShellEnvironmentPolicy{
			Inherit:              ShellEnvironmentPolicyInheritAll,
			ApplyDefaultExcludes: true,
			Exclude:              []string{"sandbox_restore"},
			Set:                  map[string]string{"SANDBOX_RESTORE": "set"},
		}),
	)
	ws := codeexecutor.Workspace{ID: "s1", Path: t.TempDir()}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		t.Fatal(err)
	}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if !hasEnv(env, "SANDBOX_RESTORE=set") {
		t.Fatalf("expected Set to run after Exclude: %v", env)
	}
	if hasEnvPrefix(env, "SANDBOX_SECRET_TOKEN=") {
		t.Fatalf("default secret-name excludes did not remove token env: %v", env)
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
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected ErrPathDenied, got %v", err)
	}
}

func TestPutFilesRejectsSymlinkRedirect(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "put/symlink-redirect", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, ".git", "config"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(ws.Path, "work", "redirect.txt")
	if err := os.Symlink(filepath.Join(ws.Path, ".git", "config"), redirect); err != nil {
		t.Fatal(err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/redirect.txt",
		Content: []byte("bad"),
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("PutFiles symlink redirect error = %v, want ErrPathDenied", err)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("protected target was written through symlink: %q", data)
	}
}

func TestProtectedMetadataOnlyCoversWorkspaceRoot(t *testing.T) {
	protected := defaultProtectedMetadata()
	for _, rel := range []string{".git", ".git/config", ".agents/skills/demo/SKILL.md"} {
		if !isProtectedRel(rel, protected) {
			t.Fatalf("expected %q to be protected", rel)
		}
	}
	for _, rel := range []string{
		".codex/settings.json",
		"vendor/.git/config",
		"submodule/.agents/skills/demo/SKILL.md",
	} {
		if isProtectedRel(rel, protected) {
			t.Fatalf("expected %q not to be protected", rel)
		}
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
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected no-access glob to block Collect, got %v", err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/app.env",
		Content: []byte("TOKEN=new"),
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected no-access glob to block PutFiles, got %v", err)
	}
}

func TestCollectRejectsSymlinkResolvedDeniedTarget(t *testing.T) {
	profile := WorkspaceWriteProfile().
		WithNoAccessPaths("work/secret.txt").
		WithNoAccessGlobs("work/*.env")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "collect/symlink-denied", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "app.env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("secret.txt", filepath.Join(ws.Path, "work", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/link.txt"}); !isKind(err, ErrPathDenied) {
		t.Fatalf("expected resolved no-access path to block Collect, got %v", err)
	}
	if err := os.Symlink("app.env", filepath.Join(ws.Path, "work", "env-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/env-link.txt"}); !isKind(err, ErrPathDenied) {
		t.Fatalf("expected resolved no-access glob to block Collect, got %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws.Path, "work", "outside-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/outside-link.txt"}); !isKind(err, ErrPathDenied) {
		t.Fatalf("expected outside symlink target to block Collect, got %v", err)
	}
}

func TestCollectOutputsRejectsSymlinkResolvedDeniedTarget(t *testing.T) {
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/secret.txt")
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "collect/outputs-symlink-denied", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../work/secret.txt", filepath.Join(ws.Path, "out", "leak.txt")); err != nil {
		t.Fatal(err)
	}
	_, err = rt.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{
		Globs:  []string{"out/leak.txt"},
		Inline: true,
	})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected resolved no-access path to block CollectOutputs, got %v", err)
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
	if _, err := rt.Collect(context.Background(), ws, []string{"work/secret.txt"}); !isKind(err, ErrPathDenied) {
		t.Fatalf("expected AccessNone to deny read, got %v", err)
	}
	err = rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/secret.txt",
		Content: []byte("new"),
	}})
	if !isKind(err, ErrPathDenied) {
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
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected more specific read rule to deny write, got %v", err)
	}
}

func TestEqualSpecificityAccessPrecedence(t *testing.T) {
	profile := WorkspaceWriteProfile()
	profile.fileSystem.Rules = append(profile.fileSystem.Rules,
		fileSystemRule{Kind: rulePath, Access: accessRead, Path: "work/tie.txt"},
		fileSystemRule{Kind: rulePath, Access: accessWrite, Path: "work/tie.txt"},
		fileSystemRule{Kind: rulePath, Access: accessNone, Path: "work/tie.txt"},
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
	if d.access != accessNone {
		t.Fatalf("equal-specificity access = %s, want %s", d.access, accessNone)
	}
	if _, err := rt.Collect(context.Background(), ws, []string{"work/tie.txt"}); !isKind(err, ErrPathDenied) {
		t.Fatalf("expected equal-specificity AccessNone to deny read, got %v", err)
	}
}

func TestRuleGlobOnlySupportsAccessNone(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "glob-shape", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []fileSystemAccess{accessRead, accessWrite} {
		profile := WorkspaceWriteProfile()
		profile.fileSystem.Rules = append(profile.fileSystem.Rules, fileSystemRule{
			Kind: ruleGlob, Access: access, Glob: "work/*.env",
		})
		err := rt.checkRead(profile, ws, codeexecutor.DirWork)
		if !isKind(err, ErrPolicyViolation) {
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
	profile.fileSystem.Rules = append(profile.fileSystem.Rules, fileSystemRule{
		Kind: ruleSpecial, Access: accessNone, Special: specialOut,
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
	if containsRule(withRead, accessNone, "work/none") {
		t.Fatalf("WithNoAccessPaths mutated sibling profile rules: %#v", withRead.fileSystem.Rules)
	}
	if containsRule(withNone, accessRead, "work/read") {
		t.Fatalf("WithReadPaths mutated sibling profile rules: %#v", withNone.fileSystem.Rules)
	}
	if containsRule(base, accessRead, "work/read") || containsRule(base, accessNone, "work/none") {
		t.Fatalf("builder mutated base profile rules: %#v", base.fileSystem.Rules)
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

func TestSessionPolicyDefaultsAndExplicitZero(t *testing.T) {
	defaultRuntime := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if defaultRuntime.sessionPolicy.Persistence != SessionPersistencePerSession ||
		defaultRuntime.sessionPolicy.RunConcurrency != SessionRunConcurrencySerial {
		t.Fatalf("default session policy = %#v, want per-session/serial", defaultRuntime.sessionPolicy)
	}

	explicitZero := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithSessionPolicy(SessionPolicy{}),
	)
	if explicitZero.sessionPolicy.Persistence != SessionPersistencePerTurn ||
		explicitZero.sessionPolicy.RunConcurrency != SessionRunConcurrencyParallel {
		t.Fatalf("explicit zero session policy = %#v, want per-turn/parallel", explicitZero.sessionPolicy)
	}
	ws, err := explicitZero.CreateWorkspace(context.Background(), "explicit-zero", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := explicitZero.Cleanup(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Fatalf("explicit non-persistent cleanup kept workspace, stat err=%v", err)
	}

	serialOnly := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithSessionPolicy(SessionPolicy{RunConcurrency: SessionRunConcurrencySerial}),
	)
	if serialOnly.sessionPolicy.Persistence != SessionPersistencePerTurn ||
		serialOnly.sessionPolicy.RunConcurrency != SessionRunConcurrencySerial {
		t.Fatalf("serial-only session policy = %#v, want per-turn/serial", serialOnly.sessionPolicy)
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

	collidingPath, collidingID := workspacePathForID(root, "app/user:a/session")
	plainPath, plainID := workspacePathForID(root, "app/user_a/session")
	if collidingPath == plainPath || collidingID == plainID {
		t.Fatalf("workspace IDs collided: %s/%s vs %s/%s", collidingPath, collidingID, plainPath, plainID)
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
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected read denial without grant, got %v", err)
	}
	grantCtx := WithAdditionalPermissions(ctx, AdditionalPermissions{
		ReadPaths: []string{hostFile},
	})
	if err := rt.StageDirectory(grantCtx, ws, hostFile, "work/granted.txt", codeexecutor.StageOptions{}); err != nil {
		t.Fatal(err)
	}
	err = rt.StageDirectory(ctx, ws, hostFile, "work/no-grant-again.txt", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("expected grant to be scoped to one context, got %v", err)
	}
}

func TestStageDirectoryValidatesCopiedTargets(t *testing.T) {
	ctx := context.Background()
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host, ".git", "config"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths(host)),
	)
	ws, err := rt.CreateWorkspace(ctx, "stage/protected-child", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.StageDirectory(ctx, ws, host, ".", codeexecutor.StageOptions{}); !isKind(err, ErrPathDenied) {
		t.Fatalf("protected child stage error = %v, want ErrPathDenied", err)
	}

	secretHost := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretHost, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	noAccess := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(
			WorkspaceWriteProfile().
				WithReadPaths(secretHost).
				WithNoAccessPaths("work/staged/secret.txt"),
		),
	)
	noAccessWS, err := noAccess.CreateWorkspace(ctx, "stage/no-access-child", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = noAccess.StageDirectory(ctx, noAccessWS, secretHost, "work/staged", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("no-access child stage error = %v, want ErrPathDenied", err)
	}

	redirectHost := t.TempDir()
	if err := os.WriteFile(filepath.Join(redirectHost, "redirect.txt"), []byte("redirect"), 0o600); err != nil {
		t.Fatal(err)
	}
	redirect := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths(redirectHost)),
	)
	redirectWS, err := redirect.CreateWorkspace(ctx, "stage/symlink-redirect", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(redirectWS.Path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(redirectWS.Path, "work", "staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(redirectWS.Path, ".git", "config"),
		filepath.Join(redirectWS.Path, "work", "staged", "redirect.txt"),
	); err != nil {
		t.Fatal(err)
	}
	err = redirect.StageDirectory(ctx, redirectWS, redirectHost, "work/staged", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("symlink redirect stage error = %v, want ErrPathDenied", err)
	}

	symlinkHost := t.TempDir()
	symlinkTarget := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(symlinkTarget, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(symlinkTarget, filepath.Join(symlinkHost, "outside-link.txt")); err != nil {
		t.Fatal(err)
	}
	symlinkRuntime := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths(symlinkHost)),
	)
	symlinkWS, err := symlinkRuntime.CreateWorkspace(ctx, "stage/source-symlink", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = symlinkRuntime.StageDirectory(ctx, symlinkWS, symlinkHost, "work/staged", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("source symlink stage error = %v, want ErrPathDenied", err)
	}
}

func TestStageInputsWorkspaceAndSkillValidateCopiedTargets(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(ctx, "stage/input-target-policy", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Path, "work", "source", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, "work", "source", ".git", "config"),
		[]byte("bad"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "workspace://work/source",
		To:   ".",
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("workspace input stage error = %v, want ErrPathDenied", err)
	}

	if err := os.MkdirAll(filepath.Join(ws.Path, codeexecutor.DirSkills, "demo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, codeexecutor.DirSkills, "demo", ".git", "config"),
		[]byte("bad"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "skill://demo",
		To:   ".",
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("skill input stage error = %v, want ErrPathDenied", err)
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
	if !isKind(err, ErrTimeout) {
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

func containsRule(profile PermissionProfile, access fileSystemAccess, path string) bool {
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind == rulePath && rule.Access == access && rule.Path == path {
			return true
		}
	}
	return false
}

func containsSpecialRule(profile PermissionProfile, access fileSystemAccess, special specialPath) bool {
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind == ruleSpecial && rule.Access == access && rule.Special == special {
			return true
		}
	}
	return false
}
