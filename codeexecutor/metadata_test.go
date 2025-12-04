//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureLayout_LoadSaveMetadata(t *testing.T) {
	root := t.TempDir()

	// Ensure layout creates dirs and metadata.json.
	paths, err := EnsureLayout(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, DirSkills), paths[DirSkills])
	require.Equal(t, filepath.Join(root, DirWork), paths[DirWork])
	require.Equal(t, filepath.Join(root, DirRuns), paths[DirRuns])
	require.Equal(t, filepath.Join(root, DirOut), paths[DirOut])

	// Loading existing metadata should succeed.
	md, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotZero(t, md.CreatedAt.Unix())

	// Modify and save metadata, then reload to verify roundtrip.
	md.Inputs = append(md.Inputs, InputRecord{
		From:      "host://x",
		To:        "work/y",
		Mode:      "copy",
		Timestamp: time.Now(),
	})
	require.NoError(t, SaveMetadata(root, md))
	md2, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, md.Version, md2.Version)
	require.Equal(t, len(md.Inputs), len(md2.Inputs))
}

func TestLoadMetadata_MissingFileReturnsDefault(t *testing.T) {
	root := t.TempDir()
	// No metadata.json yet.
	md, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.Empty(t, md.Inputs)
}

func TestDirDigest_DeterministicAndSensitive(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "a", "b"), 0o755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "b", "x.txt"), []byte("one"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "c.txt"), []byte("two"), 0o644,
	))

	d1, err := DirDigest(root)
	require.NoError(t, err)
	// Recompute should match.
	d2, err := DirDigest(root)
	require.NoError(t, err)
	require.Equal(t, d1, d2)

	// Changing a file should change digest.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "c.txt"), []byte("changed"), 0o644,
	))
	d3, err := DirDigest(root)
	require.NoError(t, err)
	require.NotEqual(t, d1, d3)
}

func TestEnsureLayout_PathConflict_Error(t *testing.T) {
	root := t.TempDir()
	// Create a file that conflicts with a required directory name.
	// MkdirAll should fail when hitting a file path.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, DirSkills), []byte("x"), 0o644,
	))
	_, err := EnsureLayout(root)
	require.Error(t, err)
}

func TestLoadMetadata_InvalidJSON_Error(t *testing.T) {
	root := t.TempDir()
	// Write a bogus metadata.json.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, MetaFileName), []byte("not-json"), 0o644,
	))
	_, err := LoadMetadata(root)
	require.Error(t, err)
}

func TestSaveMetadata_PathIsFile_Error(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "asfile")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o644))
	err := SaveMetadata(root, WorkspaceMetadata{Version: 1})
	require.Error(t, err)
}
