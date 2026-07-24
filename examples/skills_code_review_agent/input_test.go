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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitWorkingTreeDiffIncludesTrackedAndUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	runTestGit(t, repo, "init")
	runTestGit(t, repo, "config", "user.name", "review-test")
	runTestGit(t, repo, "config", "user.email", "review@example.com")
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "main.go"),
		[]byte("package main\n\nfunc value() int { return 1 }\n"),
		0o644,
	))
	runTestGit(t, repo, "add", "main.go")
	runTestGit(t, repo, "commit", "-m", "initial")

	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "main.go"),
		[]byte("package main\n\nfunc value() int { return 2 }\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, "extra.go"),
		[]byte("package main\n\nfunc extra() {}\n"),
		0o644,
	))

	diff, err := gitWorkingTreeDiff(repo, nil)
	require.NoError(t, err)
	parsed, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, parsed.Files, 2)

	selected, err := gitWorkingTreeDiff(repo, []string{"main.go"})
	require.NoError(t, err)
	parsed, err = ParseUnifiedDiff(selected)
	require.NoError(t, err)
	require.Len(t, parsed.Files, 1)
	require.Equal(t, "main.go", parsed.Files[0].Path)
}

func runTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
