//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	migrateApp     = "test-app"
	migrateUser    = "test-user"
	migrateSession = "test-session"
)

// migrateKeyPair returns the new and legacy workspace keys for the shared
// test session triple.
func migrateKeyPair(t *testing.T) (newKey, legacyKey string) {
	t.Helper()
	legacyKey = codeexecutor.LegacySessionWorkspaceKey(
		migrateApp, migrateUser, migrateSession)
	if legacyKey != "test-app/test-user/test-session" {
		t.Fatalf("legacy key = %q, want test-app/test-user/test-session", legacyKey)
	}
	newKey = codeexecutor.SessionWorkspaceKey(
		migrateApp, migrateUser, migrateSession)
	if newKey == "" || newKey == legacyKey {
		t.Fatalf("new key = %q, want non-empty and distinct from legacy", newKey)
	}
	return newKey, legacyKey
}

// seedLegacyWorkspace creates a legacy-layout workspace containing work/out
// files and returns its path.
func seedLegacyWorkspace(t *testing.T, root, legacyKey string) string {
	t.Helper()
	legacyPath, _ := workspacePathForID(root, legacyKey)
	if err := os.MkdirAll(filepath.Join(legacyPath, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(legacyPath, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyPath, "work", "source.txt"),
		[]byte("legacy-work"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyPath, "out", "result.txt"),
		[]byte("legacy-out"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	return legacyPath
}

func assertMigratedFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, data, want)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing, stat err=%v", path, err)
	}
}

func TestMigrateLegacyWorkspace_MigratesOldDirectory(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	if err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey}); err != nil {
		t.Fatalf("migrateLegacyWorkspace error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

func TestMigrateLegacyWorkspace_NoLegacyIsNoop(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	rt := NewRuntime(WithWorkspaceRoot(root))

	if err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey}); err != nil {
		t.Fatalf("migrateLegacyWorkspace without legacy dir error = %v", err)
	}

	// Migration must not create anything on its own.
	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
}

func TestMigrateLegacyWorkspace_AlreadyMigrated(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)

	// A new-style workspace already exists for the session (freshly
	// created on the new layout, or migrated by an earlier call).
	newPath, _ := workspacePathForID(root, newKey)
	if err := os.MkdirAll(filepath.Join(newPath, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(newPath, "work", "source.txt"),
		[]byte("new-work"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	if err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey}); err != nil {
		t.Fatalf("migrateLegacyWorkspace with existing destination error = %v", err)
	}

	// The legacy directory is preserved untouched, not overwritten.
	assertPathExists(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(legacyPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "new-work")
}

func TestMigrateLegacyWorkspace_ConcurrentRace(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	// Both goroutines race to rename the same legacy directory. Exactly
	// one rename can win; on Windows the loser's os.Rename fails because
	// the destination exists, and on POSIX it fails with ENOTEMPTY since
	// the migrated directory holds the seeded work/out content. In both
	// cases the loser observes the destination and returns nil.
	const workers = 2
	var wg sync.WaitGroup
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d migrate error = %v", i, err)
		}
	}
	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

func TestCreateWorkspace_MigratesLegacySessionWorkspace(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	ctx := withLegacyWorkspaceKey(context.Background(), legacyKey)
	ws, err := rt.CreateWorkspace(ctx, newKey, codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatalf("CreateWorkspace error = %v", err)
	}

	wantPath, _ := workspacePathForID(root, newKey)
	if ws.Path != wantPath {
		t.Fatalf("workspace path = %q, want %q", ws.Path, wantPath)
	}
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(ws.Path, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(ws.Path, "out", "result.txt"), "legacy-out")
}

// migrateInvocationContext builds an invocation context for the shared test
// session triple without attaching any sandbox-private context value — the
// same shape the flow processor, workspacesession.Resolver, and openclaw
// produce when they derive the workspace ID from the invocation.
func migrateInvocationContext() context.Context {
	return agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			AppName: migrateApp,
			UserID:  migrateUser,
			ID:      migrateSession,
		},
	})
}

// TestCreateWorkspace_MigratesLegacySessionWorkspace_ByShape is the
// framework-real regression: callers pass
// workspacesession.KeyFromInvocation(invocation) — i.e.
// codeexecutor.SessionWorkspaceKey(app, user, id) — as the workspace ID
// with no private context value attached. CreateWorkspace must recognize
// that shape, derive the legacy key from the invocation, and migrate.
func TestCreateWorkspace_MigratesLegacySessionWorkspace_ByShape(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	ws, err := rt.CreateWorkspace(
		migrateInvocationContext(), newKey, codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatalf("CreateWorkspace error = %v", err)
	}

	wantPath, _ := workspacePathForID(root, newKey)
	if ws.Path != wantPath {
		t.Fatalf("workspace path = %q, want %q", ws.Path, wantPath)
	}
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(ws.Path, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(ws.Path, "out", "result.txt"), "legacy-out")
}

// TestCreateWorkspace_NoMigrationForForeignExecID verifies that a workspace
// ID which is not the invocation's session key (caller-chosen explicit IDs,
// ephemeral keys) never triggers migration: those IDs are identical on old
// and new binaries, so no differently-named legacy directory exists.
func TestCreateWorkspace_NoMigrationForForeignExecID(t *testing.T) {
	root := t.TempDir()
	_, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	ws, err := rt.CreateWorkspace(
		migrateInvocationContext(), "custom-exec-id", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatalf("CreateWorkspace error = %v", err)
	}

	if ws.Path == legacyPath {
		t.Fatalf("workspace unexpectedly reused legacy path %q", ws.Path)
	}
	// The legacy directory belongs to a different key and must stay put.
	assertPathExists(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(legacyPath, "work", "source.txt"), "legacy-work")
}

func TestCreateWorkspace_PerTurnPolicySkipsMigration(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(
		WithWorkspaceRoot(root),
		WithSessionPolicy(SessionPolicy{Persistence: SessionPersistencePerTurn}),
	)

	ctx := withLegacyWorkspaceKey(context.Background(), legacyKey)
	ws, err := rt.CreateWorkspace(ctx, newKey, codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatalf("CreateWorkspace error = %v", err)
	}

	// Per-turn workspaces are not reused across runs, so the legacy
	// directory must be left untouched instead of migrated.
	if ws.Path == legacyPath {
		t.Fatalf("per-turn workspace reused legacy path %q", ws.Path)
	}
	assertPathExists(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(legacyPath, "work", "source.txt"), "legacy-work")
}

func TestExecuteCode_MigratesLegacySessionWorkspace(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	e := New(
		WithWorkspaceRoot(root),
		WithPermissionProfile(DangerFullAccessProfile()),
	)

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			AppName: migrateApp,
			UserID:  migrateUser,
			ID:      migrateSession,
		},
	})
	// Program availability is environment-dependent (e.g. bash may be
	// absent on Windows); run failures are captured into the output, so
	// only the filesystem effect of the workspace acquisition — the
	// legacy-to-new migration — is asserted here.
	if _, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo upgraded"},
		},
	}); err != nil {
		t.Fatalf("ExecuteCode error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

// TestExecuteCode_MigratesLegacySessionWorkspace_ExplicitKey is the
// processor-shape regression: internal/flow/processor/codeexecution.go
// always writes workspacesession.KeyFromInvocation(invocation) — i.e.
// codeexecutor.SessionWorkspaceKey(app, user, id) — into ExecutionID.
// Migration must trigger for that path too, not only for the empty-ID
// fallback.
func TestExecuteCode_MigratesLegacySessionWorkspace_ExplicitKey(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	e := New(
		WithWorkspaceRoot(root),
		WithPermissionProfile(DangerFullAccessProfile()),
	)

	if _, err := e.ExecuteCode(migrateInvocationContext(),
		codeexecutor.CodeExecutionInput{
			ExecutionID: newKey,
			CodeBlocks: []codeexecutor.CodeBlock{
				{Language: "bash", Code: "echo upgraded"},
			},
		}); err != nil {
		t.Fatalf("ExecuteCode error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

// TestExecuteCode_MigratesLegacySessionWorkspace_AppOnlyTenant covers the
// app-only historical form: the pre-change sandbox executor joined each
// non-empty session field, persisting (app, "", id) under "app/id". Direct
// ExecuteCode calls with that session must migrate the "app/id" directory,
// not orphan it behind a fresh sess-<hex> workspace.
func TestExecuteCode_MigratesLegacySessionWorkspace_AppOnlyTenant(t *testing.T) {
	root := t.TempDir()
	newKey := codeexecutor.SessionWorkspaceKey(migrateApp, "", migrateSession)
	if newKey == "" {
		t.Fatal("SessionWorkspaceKey returned empty for app-only session")
	}
	legacyPath := seedLegacyWorkspace(t, root, migrateApp+"/"+migrateSession)
	e := New(
		WithWorkspaceRoot(root),
		WithPermissionProfile(DangerFullAccessProfile()),
	)

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			AppName: migrateApp,
			ID:      migrateSession,
		},
	})
	if _, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo upgraded"},
		},
	}); err != nil {
		t.Fatalf("ExecuteCode error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

// TestExecuteCode_MigratesLegacySessionWorkspace_UserOnlyTenant is the
// user-only counterpart: the pre-change sandbox executor persisted
// ("", user, id) under "user/id", and that directory must be migrated too.
func TestExecuteCode_MigratesLegacySessionWorkspace_UserOnlyTenant(t *testing.T) {
	root := t.TempDir()
	newKey := codeexecutor.SessionWorkspaceKey("", migrateUser, migrateSession)
	if newKey == "" {
		t.Fatal("SessionWorkspaceKey returned empty for user-only session")
	}
	legacyPath := seedLegacyWorkspace(t, root, migrateUser+"/"+migrateSession)
	e := New(
		WithWorkspaceRoot(root),
		WithPermissionProfile(DangerFullAccessProfile()),
	)

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			UserID: migrateUser,
			ID:     migrateSession,
		},
	})
	if _, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo upgraded"},
		},
	}); err != nil {
		t.Fatalf("ExecuteCode error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

// TestWorkspaceRegistryAcquire_MigratesLegacyWorkspace is the registry-path
// regression: skill_run (via workspacesession.Resolver) and openclaw acquire
// PerSession workspaces through WorkspaceRegistry.Acquire with the hashed
// session key and no sandbox-private context value. The registry creates the
// workspace on a context.WithoutCancel(ctx) goroutine; that context must
// still carry the invocation so shape-based migration fires there too.
func TestWorkspaceRegistryAcquire_MigratesLegacyWorkspace(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath := seedLegacyWorkspace(t, root, legacyKey)
	rt := NewRuntime(WithWorkspaceRoot(root))

	reg := codeexecutor.NewWorkspaceRegistry()
	ws, err := reg.Acquire(migrateInvocationContext(), rt, newKey)
	if err != nil {
		t.Fatalf("Acquire error = %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	if ws.Path != newPath {
		t.Fatalf("workspace path = %q, want %q", ws.Path, newPath)
	}
	assertPathExists(t, newPath)
	assertPathMissing(t, legacyPath)
	assertMigratedFileContent(
		t, filepath.Join(newPath, "work", "source.txt"), "legacy-work")
	assertMigratedFileContent(
		t, filepath.Join(newPath, "out", "result.txt"), "legacy-out")
}

// TestMigrateLegacyWorkspace_SymlinkedLegacyRootRejected guards against the
// migration following a legacy root that is itself a symlink. If the legacy
// path is a symlink pointing outside the configured workspace root,
// os.Rename would move the link and subsequent layout creation would write
// through it, escaping the root. Migration must refuse to move it AND must
// surface an error so CreateWorkspace does not silently continue with a
// fresh workspace while persisted state sits behind the symlink.
func TestMigrateLegacyWorkspace_SymlinkedLegacyRootRejected(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "sensitive.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath, _ := workspacePathForID(root, legacyKey)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, legacyPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
	if err == nil {
		t.Fatal("migrateLegacyWorkspace on symlinked legacy root = nil, want error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should identify the symlinked root: %v", err)
	}

	// The symlink must be left in place, the new workspace must not be
	// created through it, and nothing must be written outside the root.
	assertPathExists(t, legacyPath)
	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
	if data, err := os.ReadFile(filepath.Join(outside, "sensitive.txt")); err != nil || string(data) != "secret" {
		t.Fatalf("outside file altered: content=%q err=%v", data, err)
	}
}

// TestMigrateLegacyWorkspace_NonDirectoryLegacyRootFails verifies that a
// legacy path which exists but is not a directory (e.g. a regular file) is
// an unexpected type and fails migration instead of being skipped silently.
func TestMigrateLegacyWorkspace_NonDirectoryLegacyRootFails(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	legacyPath, _ := workspacePathForID(root, legacyKey)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
	if err == nil {
		t.Fatal("migrateLegacyWorkspace on non-directory legacy root = nil, want error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should identify the non-directory root: %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
	// The unexpected file must be preserved, not consumed.
	assertPathExists(t, legacyPath)
}

// TestMigrateLegacyWorkspace_PreexistingSymlinkedDestinationRejected guards
// the already-completed-migration branch: when the deterministic sess-*
// destination already exists as a symlink pointing outside the configured
// root, migration must not treat it (via a following Stat) as a completed
// upgrade and return success — CreateWorkspace would then run MkdirAll and
// EnsureLayout through the link, writing outside the root. The destination
// must be validated with Lstat and rejected.
func TestMigrateLegacyWorkspace_PreexistingSymlinkedDestinationRejected(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "sensitive.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedLegacyWorkspace(t, root, legacyKey)

	newPath, _ := workspacePathForID(root, newKey)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, newPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
	if err == nil {
		t.Fatal("migrateLegacyWorkspace with symlinked destination = nil, want error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should identify the symlinked destination: %v", err)
	}

	// The legacy workspace must be preserved untouched (no rename onto
	// the symlink) and nothing must be written outside the root.
	legacyPath, _ := workspacePathForID(root, legacyKey)
	assertPathExists(t, legacyPath)
	if data, err := os.ReadFile(filepath.Join(outside, "sensitive.txt")); err != nil || string(data) != "secret" {
		t.Fatalf("outside file altered: content=%q err=%v", data, err)
	}
}

// TestMigrateLegacyWorkspace_PreexistingNonDirectoryDestinationFails
// verifies that a pre-existing destination that is a regular file (not a
// directory) is rejected rather than accepted as a completed migration.
func TestMigrateLegacyWorkspace_PreexistingNonDirectoryDestinationFails(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	seedLegacyWorkspace(t, root, legacyKey)

	newPath, _ := workspacePathForID(root, newKey)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
	if err == nil {
		t.Fatal("migrateLegacyWorkspace with non-directory destination = nil, want error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should identify the non-directory destination: %v", err)
	}

	// The legacy workspace must be preserved, not renamed over the file.
	legacyPath, _ := workspacePathForID(root, legacyKey)
	assertPathExists(t, legacyPath)
}

// TestMigrateLegacyWorkspace_AmbiguousLegacyFormsFail covers sessions whose
// identity is partially populated: the pre-change sandbox executor joined
// each non-empty field ("app/id"), while the processor/resolver path used
// just "id". When BOTH forms exist on disk, migration cannot decide which
// one holds the session state and must fail instead of guessing.
func TestMigrateLegacyWorkspace_AmbiguousLegacyFormsFail(t *testing.T) {
	root := t.TempDir()
	newKey := codeexecutor.SessionWorkspaceKey(migrateApp, "", migrateSession)
	if newKey == "" {
		t.Fatal("SessionWorkspaceKey returned empty for app-only session")
	}
	joinedForm := seedLegacyWorkspace(t, root, migrateApp+"/"+migrateSession)
	idOnlyForm := seedLegacyWorkspace(t, root, migrateSession)
	rt := NewRuntime(WithWorkspaceRoot(root))

	err := rt.migrateLegacyWorkspace(
		newKey, legacyWorkspaceKeyCandidates(migrateApp, "", migrateSession))
	if err == nil {
		t.Fatal("migrateLegacyWorkspace with both legacy forms = nil, want ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error should identify the ambiguity: %v", err)
	}

	// Nothing migrated or deleted; both legacy directories stay put.
	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
	assertPathExists(t, joinedForm)
	assertPathExists(t, idOnlyForm)
}

// TestMigrateLegacyWorkspace_IntermediateLegacySymlinkRejected guards
// the non-final-component case: Lstat(oldPath) only describes the last
// segment, so an intermediate key component such as root/sandbox/app
// can be a symlink to an outside directory while oldPath still looks
// like a plain directory. os.Rename would then move that outside
// directory into sess-*. Every ancestor beneath the workspace root
// must be a plain directory.
func TestMigrateLegacyWorkspace_IntermediateLegacySymlinkRejected(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	outside := t.TempDir()
	escaped := filepath.Join(outside, "test-user", "test-session")
	if err := os.MkdirAll(filepath.Join(escaped, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sensitive.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(escaped, "work", "source.txt"),
		[]byte("legacy-work"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	appComponent := filepath.Join(root, "sandbox", "test-app")
	if err := os.Symlink(outside, appComponent); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	err := rt.migrateLegacyWorkspace(newKey, []string{legacyKey})
	if err == nil {
		t.Fatal("migrateLegacyWorkspace on intermediate legacy symlink = nil, want error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should identify the intermediate symlink: %v", err)
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
	assertPathExists(t, appComponent)
	assertPathExists(t, filepath.Join(escaped, "work", "source.txt"))
	if data, err := os.ReadFile(filepath.Join(outside, "sensitive.txt")); err != nil || string(data) != "secret" {
		t.Fatalf("outside file altered: content=%q err=%v", data, err)
	}
}

// TestMigrateLegacyWorkspace_CompletedDestinationIgnoresLegacyAmbiguity
// is the destination-first contract: when a valid sess-* directory
// already exists, coexisting historical key forms must not make the
// upgraded session unusable. Probe runs only when migration is
// actually required.
func TestMigrateLegacyWorkspace_CompletedDestinationIgnoresLegacyAmbiguity(t *testing.T) {
	root := t.TempDir()
	newKey := codeexecutor.SessionWorkspaceKey(migrateApp, "", migrateSession)
	if newKey == "" {
		t.Fatal("SessionWorkspaceKey returned empty for app-only session")
	}
	joinedForm := seedLegacyWorkspace(t, root, migrateApp+"/"+migrateSession)
	idOnlyForm := seedLegacyWorkspace(t, root, migrateSession)

	newPath, _ := workspacePathForID(root, newKey)
	if err := os.MkdirAll(filepath.Join(newPath, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(newPath, "work", "source.txt"),
		[]byte("new-work"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	if err := rt.migrateLegacyWorkspace(
		newKey, legacyWorkspaceKeyCandidates(migrateApp, "", migrateSession),
	); err != nil {
		t.Fatalf("already-upgraded destination with two legacy forms = %v, want nil", err)
	}

	assertPathExists(t, joinedForm)
	assertPathExists(t, idOnlyForm)
	assertMigratedFileContent(t, filepath.Join(newPath, "work", "source.txt"), "new-work")
}

// TestValidateMigratedWorkspace_RejectsSymlinkAndNonDirectory unit-tests the
// post-rename revalidation: a freshly migrated path must be a plain
// directory. This is the guard for the Lstat→Rename swap window: if the
// source is replaced by a symlink between the probe and the rename, the
// moved destination is a link and layout creation must not write through it.
func TestValidateMigratedWorkspace_RejectsSymlinkAndNonDirectory(t *testing.T) {
	dir := t.TempDir()

	realDir := filepath.Join(dir, "real-dir")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateMigratedWorkspace(realDir); err != nil {
		t.Fatalf("validateMigratedWorkspace on real directory = %v, want nil", err)
	}

	plainFile := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(plainFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateMigratedWorkspace(plainFile); err == nil {
		t.Fatal("validateMigratedWorkspace on regular file = nil, want error")
	}

	link := filepath.Join(dir, "symlink-to-dir")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}
	if err := validateMigratedWorkspace(link); err == nil {
		t.Fatal("validateMigratedWorkspace on symlink = nil, want error")
	}
}

func TestLegacyWorkspaceKeyCandidates_EmptyID(t *testing.T) {
	if got := legacyWorkspaceKeyCandidates(migrateApp, migrateUser, ""); got != nil {
		t.Fatalf("empty id candidates = %v, want nil", got)
	}
	if got := legacyWorkspaceKeyCandidates(migrateApp, migrateUser, "   "); got != nil {
		t.Fatalf("whitespace id candidates = %v, want nil", got)
	}
}

func TestMigrateLegacyWorkspace_EmptyInputsAreNoop(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	if err := rt.migrateLegacyWorkspace("", []string{"legacy"}); err != nil {
		t.Fatalf("empty newKey error = %v, want nil", err)
	}
	if err := rt.migrateLegacyWorkspace("sess-key", nil); err != nil {
		t.Fatalf("empty legacyKeys error = %v, want nil", err)
	}
}

func TestInspectContainedPlainDir_OutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	err := inspectContainedPlainDir(root, outside, "legacy workspace")
	if err == nil {
		t.Fatal("inspectContainedPlainDir on a path outside root = nil, want error")
	}
	if !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("error should identify the escape: %v", err)
	}
}

func TestInspectContainedPlainDir_RootIsTarget(t *testing.T) {
	root := t.TempDir()
	if err := inspectContainedPlainDir(root, root, "legacy workspace"); err != nil {
		t.Fatalf("inspectContainedPlainDir on the root directory = %v, want nil", err)
	}

	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := inspectContainedPlainDir(file, file, "legacy workspace")
	if err == nil {
		t.Fatal("inspectContainedPlainDir on a file used as root = nil, want error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should identify the non-directory root: %v", err)
	}
}

func TestInspectContainedPlainDir_IntermediateNonDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "sandbox", "test-app")
	if err := os.WriteFile(app, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "sandbox", "test-app", "test-user", "test-session")
	err := inspectContainedPlainDir(root, target, "legacy workspace")
	if err == nil {
		t.Fatal("inspectContainedPlainDir on intermediate file = nil, want error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should identify the intermediate file: %v", err)
	}
}

func TestValidateMigrationDestination_AncestorNotDirectory(t *testing.T) {
	root := t.TempDir()
	newKey, _ := migrateKeyPair(t)
	if err := os.WriteFile(filepath.Join(root, "sandbox"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	newPath, _ := workspacePathForID(root, newKey)
	exists, err := validateMigrationDestination(root, newPath)
	if err == nil {
		t.Fatal("validateMigrationDestination with file ancestor = nil, want error")
	}
	if exists {
		t.Fatal("validateMigrationDestination with file ancestor reported exists=true")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should identify the ancestor file: %v", err)
	}
}

func TestProbeLegacyWorkspace_SkipsEmptyAndMatchingKeys(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)
	newPath, _ := workspacePathForID(root, newKey)
	found, err := probeLegacyWorkspace(root, newPath, newKey, []string{"", newKey, legacyKey})
	if err != nil {
		t.Fatalf("probeLegacyWorkspace error = %v, want nil", err)
	}
	if found != "" {
		t.Fatalf("probeLegacyWorkspace found = %q, want empty (legacy dir missing)", found)
	}
}

func TestValidateMigratedWorkspace_MissingPath(t *testing.T) {
	err := validateMigratedWorkspace(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("validateMigratedWorkspace on missing path = nil, want error")
	}
}
