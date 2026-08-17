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

	if err := rt.migrateLegacyWorkspace(newKey, legacyKey); err != nil {
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

	if err := rt.migrateLegacyWorkspace(newKey, legacyKey); err != nil {
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
	if err := rt.migrateLegacyWorkspace(newKey, legacyKey); err != nil {
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
			errs[i] = rt.migrateLegacyWorkspace(newKey, legacyKey)
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
