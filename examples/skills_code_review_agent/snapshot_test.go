//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateRepoSnapshotExcludesSensitiveFiles(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(source, "safe.go"), []byte("package safe\n"), 0o644,
	))
	for _, name := range []string{
		"id_rsa", "id_ed25519", "authorized_keys",
		".netrc", ".npmrc", ".pypirc", ".git-credentials",
	} {
		require.NoError(t, os.WriteFile(
			filepath.Join(source, name), []byte("secret"), 0o600,
		))
	}

	snapshot, cleanup, err := createRepoSnapshot(source)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.FileExists(t, filepath.Join(snapshot, "safe.go"))
	for _, name := range []string{
		"id_rsa", "id_ed25519", "authorized_keys",
		".netrc", ".npmrc", ".pypirc", ".git-credentials",
	} {
		require.NoFileExists(t, filepath.Join(snapshot, name))
	}
}
