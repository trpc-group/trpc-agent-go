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
