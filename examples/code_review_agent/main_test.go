//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestParseCLIRequiresExactlyOneInput(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--diff-file", "change.patch", "--repo-path", "."},
	} {
		_, err := parseCLI(args)
		require.ErrorContains(t, err, "exactly one")
	}
	options, err := parseCLI([]string{"--diff-file", "change.patch"})
	require.NoError(t, err)
	require.Equal(t, "change.patch", options.Selection.DiffFile)
	require.Equal(t, review.ModeRuleOnly, options.Mode)
	require.Equal(t, runtimeContainer, options.Runtime)
}

func TestSnapshotsFromCompleteDiffSkipsPartialFiles(t *testing.T) {
	diff, err := input.Parse(strings.NewReader(
		"diff --git a/config.go b/config.go\n--- a/config.go\n+++ b/config.go\n" +
			"@@ -9 +9 @@\n-old\n+const apiKey = \"sk-partial-secret-value-123456\"\n"))
	require.NoError(t, err)
	snapshots, err := snapshotsFromCompleteDiff(diff)
	require.NoError(t, err)
	require.Empty(t, snapshots)
}

func TestParseCLILocalRuntimeRequiresExplicitDevelopmentFlag(t *testing.T) {
	_, err := parseCLI([]string{"--fixture", "clean.patch", "--runtime", "local"})
	require.ErrorContains(t, err, "allow-local")
	options, err := parseCLI([]string{
		"--fixture", "clean.patch", "--runtime", "local", "--allow-local",
	})
	require.NoError(t, err)
	require.True(t, options.AllowLocal)
}

func TestParseCLIValidatesModesAndRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"--fixture", "clean.patch", "--mode", "unknown"},
		{"--fixture", "clean.patch", "--runtime", "host"},
		{"--fixture", "clean.patch", "--timeout", "0s"},
	} {
		_, err := parseCLI(args)
		require.Error(t, err)
	}
}
