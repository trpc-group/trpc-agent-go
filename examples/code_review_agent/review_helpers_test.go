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

func TestReviewHelpers(t *testing.T) {
	require.Equal(t, []string{skillScriptCommand}, reviewCommands(ReviewOptions{DryRun: true}))
	require.Len(t, reviewCommands(ReviewOptions{Runtime: "fake"}), 3)
	require.Len(t, reviewCommands(ReviewOptions{RepoPath: "."}), 3)
	require.False(t, sandboxConfigError(ReviewOptions{}))
	require.False(t, sandboxConfigError(ReviewOptions{Runtime: "container"}))
	require.False(t, sandboxConfigError(ReviewOptions{Runtime: "e2b"}))
	require.False(t, sandboxConfigError(ReviewOptions{Runtime: "fake"}))
	require.True(t, sandboxConfigError(ReviewOptions{Runtime: "local"}))
	require.False(t, sandboxConfigError(ReviewOptions{Runtime: "local", AllowTrustedLocal: true}))
	require.True(t, sandboxConfigError(ReviewOptions{Runtime: "unknown"}))

	require.Equal(t, "review completed with sandbox failures; inspect findings and sandbox summary", conclusion(nil, nil, []SandboxRun{{Status: "failed"}}))
	require.Equal(t, "no high-confidence issues found", conclusion(nil, nil, nil))
	require.Equal(t, "high-confidence issues found", conclusion([]Finding{{}}, nil, nil))
	require.Equal(t, "low-confidence issues need human review", conclusion(nil, []Finding{{}}, nil))

	require.True(t, stringsEqualFold("FaKe", "fake"))
	require.False(t, stringsEqualFold("fake", "fakes"))
	require.False(t, stringsEqualFold("fake", "fail"))

	sum := DiffSummary{AddedLines: []AddedLine{{Content: `token = "abcdefghijklmnop"`}}, Raw: "raw"}
	redacted := redactDiffSummary(sum)
	require.Empty(t, redacted.Raw)
	require.NotContains(t, redacted.AddedLines[0].Content, "abcdefghijklmnop")
}
