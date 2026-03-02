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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	_, err := parseFrontMatter("hello\n")
	require.True(t, errors.Is(err, errNoFrontMatter))
}

func TestNormalizeStringAnyMap_MapAnyAny(t *testing.T) {
	in := map[any]any{
		"openclaw": map[any]any{
			"always": true,
		},
		1: "ignore",
	}
	out := normalizeStringAnyMap(in)
	require.Contains(t, out, "openclaw")
}

func TestParseOpenClawMetadata_NoMetadata(t *testing.T) {
	meta, ok, err := parseOpenClawMetadata(parsedFrontMatter{
		Name:     "x",
		Metadata: nil,
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, openClawMetadata{}, meta)
}

func TestAsString_NonString(t *testing.T) {
	require.Empty(t, asString(123))
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

func TestEvaluateRequiredAnyBins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec lookpath + chmod differs on windows")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "mybin")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", dir)

	require.Empty(t, evaluateRequiredAnyBins([]string{"mybin", "missing"}))
	require.NotEmpty(t, evaluateRequiredAnyBins([]string{"missing1"}))
}

func TestEvaluateRequiredEnv(t *testing.T) {
	t.Setenv("SKILLS_TEST_OK", "1")

	require.Empty(t, evaluateRequiredEnv([]string{"SKILLS_TEST_OK"}))
	require.NotEmpty(t, evaluateRequiredEnv([]string{"SKILLS_TEST_MISSING"}))
}

func TestEvaluateOpenClawRequirements_Always(t *testing.T) {
	ok, reason := evaluateOpenClawRequirements(
		openClawMetadata{
			Always: true,
			Requires: openClawRequires{
				Bins: []string{"definitely-missing"},
			},
		},
		nil,
	)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestRepository_GetDisabledHasReason(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needenv", `---
name: needenv
description: test
metadata:
  { "openclaw": { "requires": { "env": ["SKILLS_TEST_NEEDENV"] } } }
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)
	require.Empty(t, r.Summaries())

	_, err = r.Get("needenv")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "missing env"))
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

func TestRepository_Path(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "p", `---
name: p
description: test
---

x
`)

	r, err := NewRepository([]string{root}, WithDebug(true))
	require.NoError(t, err)
	require.True(t, r.debug)

	got, err := r.Path("p")
	require.NoError(t, err)
	exp, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.Equal(t, exp, got)

	_, err = r.Path("missing")
	require.Error(t, err)
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

func TestEvaluateOpenClawOS_Mismatch(t *testing.T) {
	allow := []string{"darwin"}
	if runtime.GOOS == "darwin" {
		allow = []string{"linux"}
	}

	ok, reason := evaluateOpenClawOS(allow)
	require.False(t, ok)
	require.Contains(t, reason, "os mismatch")
	require.Contains(t, reason, allow[0])
}

func TestNormalizeOpenClawOS_Win32(t *testing.T) {
	require.Equal(t, "windows", normalizeOpenClawOS(" win32 "))
}

func TestEvaluateSkill_MissingFileIsEligible(t *testing.T) {
	ok, reason := evaluateSkill("/path/does/not/exist/SKILL.md", nil)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestRepository_GatesOnConfig(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needcfg", `---
name: needcfg
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord.token"] } } }
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)
	require.Empty(t, r.Summaries())

	_, err = r.Get("needcfg")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing config")
}

func TestRepository_ConfigSatisfied(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needcfg", `---
name: needcfg
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord.token"] } } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithConfigKeys([]string{"channels.discord.token"}),
	)
	require.NoError(t, err)
	require.Len(t, r.Summaries(), 1)

	_, err = r.Get("needcfg")
	require.NoError(t, err)
}

func TestRepository_ConfigPrefixMatch(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needprefix", `---
name: needprefix
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord"] } } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithConfigKeys([]string{"channels.discord.token"}),
	)
	require.NoError(t, err)
	require.Len(t, r.Summaries(), 1)
}

func TestRepository_GetEmptyName(t *testing.T) {
	r := &Repository{}
	_, err := r.Get(" ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty skill name")
}

func TestRepository_GetDisabledNoReason(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "p", `---
name: p
description: test
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	_, err = r.Get("missing")
	require.Error(t, err)
	require.Equal(t, `skill "missing" is disabled`, err.Error())
}

func TestRepository_GetUsesBasePathWhenBaseDirEmpty(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "basedir", `---
name: basedir
description: test
---

run from {baseDir}
`)
	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	r.baseDirs["basedir"] = ""

	s, err := r.Get("basedir")
	require.NoError(t, err)
	require.Contains(t, s.Body, dir)
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
