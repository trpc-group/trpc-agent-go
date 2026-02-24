//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFrontMatter_OpenClawMetadata(t *testing.T) {
	content := `---
name: coding-agent
description: "Test skill"
metadata:
  {
    "openclaw": { "requires": { "anyBins": ["codex"] } },
  }
---

# Body
`
	fm, err := parseFrontMatter(content)
	require.NoError(t, err)
	require.Equal(t, "coding-agent", fm.Name)
	require.Equal(t, "Test skill", fm.Description)

	meta, ok, err := parseOpenClawMetadata(fm)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"codex"}, meta.Requires.AnyBins)
}

func TestRepository_GatesOnBins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec lookpath + chmod differs on windows")
	}

	root := t.TempDir()
	writeSkill(t, root, "needsbin", `---
name: needsbin
description: test
metadata:
  {
    "openclaw": { "requires": { "bins": ["not-a-real-bin"] } },
  }
---

hello
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	require.Empty(t, r.Summaries())
	_, err = r.Get("needsbin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
}

func TestRepository_BaseDirSubstitution(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "basedir", `---
name: basedir
description: test
---

run from {baseDir}
`)
	err := os.WriteFile(
		filepath.Join(dir, "DOC.md"),
		[]byte("doc {baseDir}\n"),
		0o644,
	)
	require.NoError(t, err)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	s, err := r.Get("basedir")
	require.NoError(t, err)
	require.Contains(t, s.Body, dir)
	require.NotContains(t, s.Body, openClawBaseDirPlaceholder)

	require.Len(t, s.Docs, 1)
	require.Contains(t, s.Docs[0].Content, dir)
}

func TestRepository_PrecedenceNoFallback(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeSkill(t, rootA, "dup", `---
name: dup
description: test
metadata:
  { "openclaw": { "os": ["win32"] } }
---

from A
`)
	writeSkill(t, rootB, "dup", `---
name: dup
description: test
---

from B
`)

	r, err := NewRepository([]string{rootA, rootB})
	require.NoError(t, err)

	// Higher-precedence skill (rootA) is ineligible on non-windows, so the
	// skill is excluded entirely (OpenClaw semantics: no fallback).
	if runtime.GOOS != "windows" {
		require.Empty(t, r.Summaries())
		_, err := r.Get("dup")
		require.Error(t, err)
		return
	}

	s, err := r.Get("dup")
	require.NoError(t, err)
	require.Contains(t, s.Body, "from A")
}

func writeSkill(t *testing.T, root, name, skillMd string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	err := os.MkdirAll(dir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(
		filepath.Join(dir, skillFileName),
		[]byte(skillMd),
		0o644,
	)
	require.NoError(t, err)
	return dir
}
