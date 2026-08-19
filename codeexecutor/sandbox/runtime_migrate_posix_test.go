//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !windows

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLegacyWorkspace_NonNotExistStatErrorPropagated guards against
// treating every Lstat failure as "nothing to migrate". When the legacy
// path cannot be stat'ed for a reason other than absence (here a parent
// that is a regular file, so Lstat fails with ENOTDIR), the error must be
// returned rather than silently creating a fresh empty workspace that
// orphans persisted state.
//
// POSIX-only: on Windows a non-directory component in the middle of a path
// resolves to ENOENT (ERROR_PATH_NOT_FOUND), so os.IsNotExist is true there
// and the propagate branch cannot be exercised deterministically.
func TestMigrateLegacyWorkspace_NonNotExistStatErrorPropagated(t *testing.T) {
	root := t.TempDir()
	newKey, legacyKey := migrateKeyPair(t)

	// Make the parent of the legacy path a regular file so Lstat(oldPath)
	// fails with ENOTDIR: a deterministic non-IsNotExist stat error.
	legacyPath, _ := workspacePathForID(root, legacyKey)
	parentDir := filepath.Dir(legacyPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(parentDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentDir, []byte("i am a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(WithWorkspaceRoot(root))
	if err := rt.migrateLegacyWorkspace(newKey, legacyKey); err == nil {
		t.Fatal("migrateLegacyWorkspace = nil, want propagated stat error")
	}

	newPath, _ := workspacePathForID(root, newKey)
	assertPathMissing(t, newPath)
}