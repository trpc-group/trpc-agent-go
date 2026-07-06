//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inputsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestReadDiffFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "fixtures", "security_secret.diff")
	src, err := Read(context.Background(), Options{DiffFile: path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeDiffFile {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeDiffFile)
	}
	if !strings.Contains(src.Diff, "diff --git") {
		t.Fatalf("Diff did not contain unified diff content")
	}
}

func TestReadFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(path, []byte("pkg/a.go\n# comment\npkg/b_test.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	src, err := Read(context.Background(), Options{FileList: path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeFileList {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeFileList)
	}
	if got, want := strings.Join(src.FileList, ","), "pkg/a.go,pkg/b_test.go"; got != want {
		t.Fatalf("FileList = %q, want %q", got, want)
	}
}

func TestReadRejectsMultipleInputSources(t *testing.T) {
	_, err := Read(context.Background(), Options{
		DiffFile: "a.diff",
		RepoPath: ".",
	})
	if err == nil || !strings.Contains(err.Error(), "choose only one input source") {
		t.Fatalf("Read() error = %v, want multiple input source error", err)
	}
}

func TestReadRepoDiffIncludesStagedAndUntrackedWithoutColor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked) error = %v", err)
	}
	runGit(t, dir, "add", "tracked.go")
	runGit(t, dir, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package main\nconst staged = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(staged) error = %v", err)
	}
	runGit(t, dir, "add", "tracked.go")
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package main\nconst untracked = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(untracked) error = %v", err)
	}
	runGit(t, dir, "config", "color.diff", "always")

	src, err := Read(context.Background(), Options{RepoPath: dir})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeRepo {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeRepo)
	}
	if !strings.Contains(src.Diff, "+const staged = true") {
		t.Fatalf("repo diff did not include staged change:\n%s", src.Diff)
	}
	if !strings.Contains(src.Diff, "diff --git a/untracked.go b/untracked.go") ||
		!strings.Contains(src.Diff, "+const untracked = true") {
		t.Fatalf("repo diff did not include untracked file:\n%s", src.Diff)
	}
	if strings.Contains(src.Diff, "\x1b[") {
		t.Fatalf("repo diff contained ANSI color escapes:\n%q", src.Diff)
	}
}

func TestReadRepoDiffHandlesUnbornHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package main\nconst staged = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(staged) error = %v", err)
	}
	runGit(t, dir, "add", "staged.go")
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package main\nconst untracked = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(untracked) error = %v", err)
	}

	src, err := Read(context.Background(), Options{RepoPath: dir})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeRepo {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeRepo)
	}
	if !strings.Contains(src.Diff, "diff --git a/staged.go b/staged.go") ||
		!strings.Contains(src.Diff, "+const staged = true") {
		t.Fatalf("repo diff did not include staged file against empty tree:\n%s", src.Diff)
	}
	if !strings.Contains(src.Diff, "diff --git a/untracked.go b/untracked.go") ||
		!strings.Contains(src.Diff, "+const untracked = true") {
		t.Fatalf("repo diff did not include untracked file:\n%s", src.Diff)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
