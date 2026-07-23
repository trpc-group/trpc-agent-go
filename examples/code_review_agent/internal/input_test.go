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
	"runtime"
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

func TestLoadReviewInputRejectsImplicitWholeRepositoryPath(t *testing.T) {
	for _, path := range []string{"", ".", "dir/.."} {
		t.Run(path, func(t *testing.T) {
			_, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: t.TempDir(), FilePaths: []string{path}})
			require.Error(t, err)
		})
	}
}

func TestQuoteGitPathTokenRoundTrip(t *testing.T) {
	for _, path := range []string{
		"a/space name.go",
		"a/quote\"name.go",
		"a/line\nbreak.go",
		"a/测试.go",
	} {
		quoted := quoteGitPathToken(path)
		decoded, trailing, err := parseGitPathToken(quoted)
		require.NoError(t, err)
		require.Empty(t, trailing)
		require.Equal(t, path, decoded)
	}
}

func TestLiteralGitPathspecDisablesMagic(t *testing.T) {
	pathspecs, err := literalGitPathspecs([]string{":(exclude)*.go"})
	require.NoError(t, err)
	require.Equal(t, []string{":(literal):(exclude)*.go"}, pathspecs)
}

func TestLoadReviewInputIncludesUntrackedFiles(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("tracked\n"), 0o600))
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	path := filepath.Join(repo, "new.go")
	require.NoError(t, os.WriteFile(path, []byte("package demo\n\nvar apiKey = \"abcdefghijklmnop\"\n"), 0o600))

	data, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo})
	require.NoError(t, err)
	require.Contains(t, string(data), "new file mode")
	require.Contains(t, string(data), "+var apiKey")
}

func TestLoadReviewInputQuotesSpecialUntrackedNames(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("tracked\n"), 0o600))
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	files := map[string]string{
		"space name.go": "package spaced\n",
	}
	if runtime.GOOS != "windows" {
		// Windows forbids quotes and control characters in filenames. These
		// cases run on Unix CI, where Git can return both names via -z.
		files["quote\"name.go"] = "package quoted\n"
		files["line\nbreak.go"] = "package newline\n"
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(repo, filepath.FromSlash(name)), []byte(content), 0o600))
	}

	data, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo})
	require.NoError(t, err)
	parsed, err := ParseDiffString(string(data))
	require.NoError(t, err)
	require.Len(t, parsed, len(files))
	paths := make(map[string]bool, len(parsed))
	for _, file := range parsed {
		paths[file.Path] = true
		require.True(t, file.IsNew)
		require.Len(t, file.AddedLines(), 1)
	}
	for name := range files {
		require.Truef(t, paths[name], "missing parsed path %q", name)
	}
}

func TestLoadReviewInputTreatsPathspecMagicLiterally(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow ':' in filenames")
	}
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
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("tracked\n"), 0o600))
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	name := ":(exclude)*.go"
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte("package literal\n"), 0o600))

	data, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo, FilePaths: []string{name}})
	require.NoError(t, err)
	parsed, err := ParseDiffString(string(data))
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	require.Equal(t, name, parsed[0].Path)
}

func TestLoadReviewInputRejectsOversizedUntrackedFileBeforeRead(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("tracked\n"), 0o600))
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	path := filepath.Join(repo, "oversized.go")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxInputDiffBytes+1))
	require.NoError(t, file.Close())

	_, _, err = LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo})
	require.ErrorContains(t, err, "exceeds")
}

func TestLoadReviewInputRejectsUntrackedSymlink(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("tracked\n"), 0o600))
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	external := filepath.Join(t.TempDir(), "sentinel.go")
	require.NoError(t, os.WriteFile(external, []byte("package stolen\n"), 0o600))
	if err := os.Symlink(external, filepath.Join(repo, "secret.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, _, err := LoadReviewInput(context.Background(), ReviewInput{RepoPath: repo})
	require.ErrorContains(t, err, "not a regular file")
}
