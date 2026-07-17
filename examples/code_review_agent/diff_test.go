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

func TestParseUnifiedDiffEmpty(t *testing.T) {
	sum, err := ParseUnifiedDiff("")
	require.NoError(t, err)
	require.Empty(t, sum.Files)
	require.Empty(t, sum.AddedLines)
}

func TestParseUnifiedDiffRejectsTraversal(t *testing.T) {
	_, err := ParseUnifiedDiff("diff --git a/../bad.go b/../bad.go\n")
	require.Error(t, err)
}

func TestParseUnifiedDiffQuotedPathAndAddedLine(t *testing.T) {
	raw := "diff --git \"a/pkg/a b.go\" \"b/pkg/a b.go\"\n--- \"a/pkg/a b.go\"\n+++ \"b/pkg/a b.go\"\n@@ -0,0 +1,1 @@\n+package pkg\n"
	sum, err := ParseUnifiedDiff(raw)
	require.NoError(t, err)
	require.Equal(t, "pkg/a b.go", sum.Files[0].NewPath)
	require.Equal(t, 1, sum.AddedLines[0].Line)
	require.Equal(t, []PackageInfo{{Dir: "pkg", Name: "pkg", GoFiles: 1}}, sum.Packages)
}

func TestParseUnifiedDiffHunkMismatch(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1,2 @@\n+package pkg\n"
	_, err := ParseUnifiedDiff(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "hunk count mismatch")
}
