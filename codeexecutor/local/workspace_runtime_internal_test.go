//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadLimitedWithCapBranches(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.bin")
	require.NoError(t, os.WriteFile(fp, []byte("abcdef"), 0o644))

	// cap <= 0 returns empty data and mime.
	b, mt, err := readLimitedWithCap(fp, 0)
	require.NoError(t, err)
	require.Equal(t, 0, len(b))
	require.Equal(t, "", mt)

	// cap of 2 truncates to 2 bytes.
	b, mt, err = readLimitedWithCap(fp, 2)
	require.NoError(t, err)
	require.Equal(t, 2, len(b))
	require.NotEmpty(t, mt)
}

func TestCopyPath_FileAndDir(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("z"), 0o644))
	dstFile := filepath.Join(dir, "dst", "a.txt")
	require.NoError(t, copyPath(srcFile, dstFile))
	out, err := os.ReadFile(dstFile)
	require.NoError(t, err)
	require.Equal(t, "z", string(out))

	// Now copy a directory.
	srcDir := filepath.Join(dir, "srcd")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "b.txt"), []byte("y"), 0o644,
	))
	dstDir := filepath.Join(dir, "dst2")
	require.NoError(t, copyPath(srcDir, dstDir))
	b, err := os.ReadFile(filepath.Join(dstDir, "b.txt"))
	require.NoError(t, err)
	require.Equal(t, "y", string(b))
}
