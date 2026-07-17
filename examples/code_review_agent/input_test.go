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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadInputVariants(t *testing.T) {
	ctx := context.Background()
	kind, raw, err := loadInput(ctx, ReviewOptions{})
	require.NoError(t, err)
	require.Equal(t, "empty", kind)
	require.Empty(t, raw)

	diff := filepath.Join(t.TempDir(), "change.diff")
	require.NoError(t, os.WriteFile(diff, []byte("diff --git a/a.go b/a.go\n"), 0o644))
	kind, raw, err = loadInput(ctx, ReviewOptions{DiffFile: diff})
	require.NoError(t, err)
	require.Equal(t, "diff_file", kind)
	require.Contains(t, raw, "diff --git")

	kind, raw, err = loadInput(ctx, ReviewOptions{FileList: "pkg/a.go,pkg/b.go"})
	require.NoError(t, err)
	require.Equal(t, "file_list", kind)
	require.Contains(t, raw, "pkg/a.go")

	_, _, err = loadInput(ctx, ReviewOptions{FileList: "../bad.go"})
	require.Error(t, err)
}

func TestGitDiffInput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package main\n"), 0o644))
	runGit(t, repo, "add", "a.go")
	runGit(t, repo, "commit", "-m", "init")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package main\n\nfunc A() {}\n"), 0o644))

	kind, raw, err := loadInput(context.Background(), ReviewOptions{RepoPath: repo})
	require.NoError(t, err)
	require.Equal(t, "repo", kind)
	require.Contains(t, raw, "func A")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestFixtureHelpers(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one.diff"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("x"), 0o644))

	raw, err := loadFixture(dir, "one")
	require.NoError(t, err)
	require.Equal(t, "x", raw)
	raw, err = loadFixture(dir, "all")
	require.NoError(t, err)
	require.Empty(t, raw)
	_, err = loadFixture(dir, "../one")
	require.Error(t, err)
	names, err := fixtureNames(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"one"}, names)
}

func TestGitTrackedFilesPreservesSpaces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	name := " leading space.go"
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte("package main\n"), 0o644))
	runGit(t, repo, "add", name)
	files, err := gitTrackedFiles(context.Background(), repo)
	require.NoError(t, err)
	require.Contains(t, files, name)
}
