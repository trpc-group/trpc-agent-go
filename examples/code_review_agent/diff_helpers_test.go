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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiffHelperBranches(t *testing.T) {
	require.Equal(t, 1, hunkCount(""))
	require.Equal(t, 3, hunkCount("3"))
	require.Equal(t, 1, hunkCount("bad"))

	_, _, err := readGitPathToken("")
	require.Error(t, err)
	_, _, err = readGitPathToken(`"unterminated`)
	require.Error(t, err)
	token, rest, err := readGitPathToken(`"a/a b.go" tail`)
	require.NoError(t, err)
	require.Equal(t, "a/a b.go", token)
	require.Equal(t, " tail", rest)

	p, deleted, err := parseHeaderPath("/dev/null")
	require.NoError(t, err)
	require.True(t, deleted)
	require.Empty(t, p)

	_, err = validateRepoPath("C:/x.go")
	require.Error(t, err)
	_, err = validateRepoPath("a\x00b.go")
	require.Error(t, err)

	name, ok := parsePackageName("package demo_1")
	require.True(t, ok)
	require.Equal(t, "demo_1", name)
	_, ok = parsePackageName("package bad-name")
	require.False(t, ok)
	_, ok = parsePackageName("not package")
	require.False(t, ok)
}

func TestParseUnifiedDiffDeletedFile(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package a\n"
	sum, err := ParseUnifiedDiff(raw)
	require.NoError(t, err)
	require.True(t, sum.Files[0].Deleted)
	require.Empty(t, sum.AddedLines)
}

func TestParseUnifiedDiffMalformedInputs(t *testing.T) {
	_, err := ParseUnifiedDiff("+++ b/a.go\n")
	require.Error(t, err)
	_, err = ParseUnifiedDiff("@@ -1 +1 @@\n")
	require.Error(t, err)
	_, err = ParseUnifiedDiff("diff --git a/a.go b/a.go extra\n")
	require.Error(t, err)
	_, err = ParseUnifiedDiff("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n?bad\n")
	require.Error(t, err)
}
