//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package input

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromDiffFile(t *testing.T) {
	tmpDir := t.TempDir()
	diffPath := filepath.Join(tmpDir, "test.diff")
	err := os.WriteFile(diffPath, []byte("diff --git a/x.go b/x.go\n+added line\n"), 0o644)
	require.NoError(t, err)

	in, err := LoadFromDiffFile(diffPath)
	require.NoError(t, err)
	assert.Equal(t, "diff_file", in.SourceType)
	assert.Contains(t, in.DiffText, "added line")
}

func TestLoadFromDiffFileNotFound(t *testing.T) {
	_, err := LoadFromDiffFile("/nonexistent/file.diff")
	assert.Error(t, err)
}

func TestLoadFromRepoPath(t *testing.T) {
	in, err := LoadFromRepoPath("/some/repo")
	require.NoError(t, err)
	assert.Equal(t, "repo_path", in.SourceType)
	assert.Equal(t, "/some/repo", in.RepoPath)
}

func TestSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "helper.go"), []byte("package main\n"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# readme\n"), 0o644)
	require.NoError(t, err)

	files, err := Snapshot(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Contains(t, files, "main.go")
	assert.Contains(t, files, "helper.go")
	assert.NotContains(t, files, "README.md")
}

func TestSnapshotEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	files, err := Snapshot(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestSnapshotWithSubdir(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	os.MkdirAll(subDir, 0o755)
	err := os.WriteFile(filepath.Join(subDir, "x.go"), []byte("package sub\n"), 0o644)
	require.NoError(t, err)

	files, err := Snapshot(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, files, "should not scan subdirectories")
}

func TestCollectFileList(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "c_test.go"), []byte("package main_test\n"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# readme\n"), 0o644)

	files, err := CollectFileList(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Contains(t, files, "a.go")
	assert.Contains(t, files, "b.go")
	assert.NotContains(t, files, "c_test.go")
	assert.NotContains(t, files, "readme.md")
}

func TestCollectFileListEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	files, err := CollectFileList(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, files)
}
