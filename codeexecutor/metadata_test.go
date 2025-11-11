//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestEnsureLayoutAndMetadata(t *testing.T) {
	root := t.TempDir()
	paths, err := codeexecutor.EnsureLayout(root)
	require.NoError(t, err)
	// All standard dirs should exist.
	for _, k := range []string{
		codeexecutor.DirSkills,
		codeexecutor.DirWork,
		codeexecutor.DirRuns,
		codeexecutor.DirOut,
	} {
		p := paths[k]
		st, err := os.Stat(p)
		require.NoError(t, err)
		require.True(t, st.IsDir())
	}
	// Metadata file should exist.
	_, err = os.Stat(filepath.Join(
		root, codeexecutor.MetaFileName,
	))
	require.NoError(t, err)
	// Call EnsureLayout again to exercise the already-present path.
	_, err = codeexecutor.EnsureLayout(root)
	require.NoError(t, err)
}

func TestLoadSaveMetadataRoundtrip(t *testing.T) {
	root := t.TempDir()
	_, err := codeexecutor.EnsureLayout(root)
	require.NoError(t, err)
	md, err := codeexecutor.LoadMetadata(root)
	require.NoError(t, err)
	require.NotNil(t, md.Skills)
	// Add a skill and save; then reload and verify.
	md.Skills["demo"] = codeexecutor.SkillMeta{
		Name:    "demo",
		RelPath: filepath.Join(codeexecutor.DirSkills, "demo"),
		Digest:  "d0",
		Mounted: true,
	}
	require.NoError(t, codeexecutor.SaveMetadata(root, md))
	md2, err := codeexecutor.LoadMetadata(root)
	require.NoError(t, err)
	s, ok := md2.Skills["demo"]
	require.True(t, ok)
	require.Equal(t, "d0", s.Digest)
}

func TestDirDigestStableAndChanges(t *testing.T) {
	dir := t.TempDir()
	// Create a file and compute digest twice.
	a := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(a, []byte("alpha"), 0o644))
	d1, err := codeexecutor.DirDigest(dir)
	require.NoError(t, err)
	d2, err := codeexecutor.DirDigest(dir)
	require.NoError(t, err)
	require.Equal(t, d1, d2)
	// Change contents and expect digest to differ.
	require.NoError(t, os.WriteFile(a, []byte("beta"), 0o644))
	d3, err := codeexecutor.DirDigest(dir)
	require.NoError(t, err)
	require.NotEqual(t, d1, d3)
}

func TestLoadMetadata_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	// Create layout and then corrupt metadata.json with invalid JSON.
	_, err := codeexecutor.EnsureLayout(root)
	require.NoError(t, err)
	mf := filepath.Join(root, codeexecutor.MetaFileName)
	// Write malformed JSON so LoadMetadata fails to unmarshal.
	require.NoError(t, os.WriteFile(mf, []byte("{"), 0o644))
	_, err = codeexecutor.LoadMetadata(root)
	require.Error(t, err)

	// Also verify SaveMetadata writes valid JSON again.
	md := codeexecutor.WorkspaceMetadata{Version: 1,
		Skills: map[string]codeexecutor.SkillMeta{},
	}
	require.NoError(t, codeexecutor.SaveMetadata(root, md))
	// Confirm it is valid JSON.
	b, err := os.ReadFile(mf)
	require.NoError(t, err)
	var tmp any
	require.NoError(t, json.Unmarshal(b, &tmp))
}

func TestLoadMetadata_NotExist_Defaults(t *testing.T) {
	root := t.TempDir()
	md, err := codeexecutor.LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)
}

func TestSaveMetadata_WriteError(t *testing.T) {
	root := t.TempDir()
	// Make directory read-only to trigger write error.
	require.NoError(t, os.Chmod(root, 0o555))
	defer os.Chmod(root, 0o755)
	err := codeexecutor.SaveMetadata(root,
		codeexecutor.WorkspaceMetadata{Version: 1})
	require.Error(t, err)
}

func TestEnsureLayout_ErrorOnFileRoot(t *testing.T) {
	// Use a file path as the root so MkdirAll fails.
	f, err := os.CreateTemp("", "notadir-*.tmp")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	_ = f.Close()
	_, err = codeexecutor.EnsureLayout(f.Name())
	require.Error(t, err)
}
