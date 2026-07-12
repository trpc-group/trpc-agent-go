//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadReviewInputDiffFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "change.diff")
	require.NoError(t, os.WriteFile(path, []byte("diff --git a/a.go b/a.go\n"), 0o600))
	data, inputType, err := LoadReviewInput(context.Background(), ReviewInput{DiffFile: path})
	require.NoError(t, err)
	require.Equal(t, "diff", inputType)
	require.Contains(t, string(data), "a.go")
}

func TestLoadReviewInputGitWorkspaceAndPathFilter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		cmd.Dir = repo
		require.NoError(t, cmd.Run())
	}
	runGit("init")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b\n"), 0o600))
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n// changed\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b\n// hidden\n"), 0o600))

	data, inputType, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo, FilePaths: []string{"a.go"}})
	require.NoError(t, err)
	require.Equal(t, "git-workspace", inputType)
	require.Contains(t, string(data), "a.go")
	require.NotContains(t, string(data), "b.go")
}

func TestLoadReviewInputRejectsEscapingPath(t *testing.T) {
	_, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: t.TempDir(), FilePaths: []string{"../secret"}})
	require.Error(t, err)
}
