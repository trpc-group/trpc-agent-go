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

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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

func intPtr(v int) *int {
	return &v
}

func TestPinnedArtifactVersion(t *testing.T) {
	const (
		name = "demo.txt"
		to   = "work/inputs/demo.txt"
	)
	md := codeexecutor.WorkspaceMetadata{
		Inputs: []codeexecutor.InputRecord{{
			From:     inputSchemeArtifact + name + "@1",
			To:       to,
			Resolved: "other",
			Version:  intPtr(1),
		}, {
			From:     inputSchemeArtifact + name + "@bad",
			To:       to,
			Resolved: "other",
			Version:  intPtr(2),
		}, {
			From:     inputSchemeHost + "/tmp/x",
			To:       to,
			Resolved: "other",
			Version:  intPtr(3),
		}, {
			From:     inputSchemeArtifact + name + "@1",
			To:       to,
			Resolved: "other",
			Version:  nil,
		}, {
			From:     inputSchemeArtifact + name + "@1",
			To:       "work/inputs/other.txt",
			Resolved: "other",
			Version:  intPtr(4),
		}},
	}
	got := pinnedArtifactVersion(md, name, to)
	require.NotNil(t, got)
	require.Equal(t, 1, *got)

	require.Nil(t, pinnedArtifactVersion(md, "", to))
	require.Nil(t, pinnedArtifactVersion(md, name, ""))
}

func TestPinnedArtifactVersion_ResolvedMatch(t *testing.T) {
	const (
		name = "demo.txt"
		to   = "work/inputs/demo.txt"
	)
	md := codeexecutor.WorkspaceMetadata{
		Inputs: []codeexecutor.InputRecord{{
			From:     inputSchemeArtifact + name + "@1",
			To:       to,
			Resolved: name,
			Version:  intPtr(9),
		}},
	}
	got := pinnedArtifactVersion(md, name, to)
	require.NotNil(t, got)
	require.Equal(t, 9, *got)
}
