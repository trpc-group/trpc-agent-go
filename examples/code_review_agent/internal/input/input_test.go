//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadFileListBuildsDiffAndMetadata(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/input\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "handler.go"), []byte("package handler\n\nfunc Serve() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("# changed files\nhandler.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	diff, ref, err := Read(Config{}, Request{FileList: listPath, RepoPath: repo})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if ref != listPath {
		t.Fatalf("input ref = %q, want %q", ref, listPath)
	}
	if !strings.Contains(string(diff), "diff --git a/handler.go b/handler.go") {
		t.Fatalf("generated diff missing handler.go header: %s", diff)
	}

	meta := Metadata(diff, repo)
	if meta.ModulePath != "example.com/input" {
		t.Fatalf("module path = %q, want example.com/input", meta.ModulePath)
	}
	if !contains(meta.ChangedGoFiles, "handler.go") {
		t.Fatalf("changed go files = %+v, want handler.go", meta.ChangedGoFiles)
	}
	if !contains(meta.PackageNames, "handler") {
		t.Fatalf("package names = %+v, want handler", meta.PackageNames)
	}
}

func TestReadFileListRejectsRepoEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("make repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("../secret.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	_, _, err := Read(Config{}, Request{FileList: listPath, RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "escapes base directory") {
		t.Fatalf("Read error = %v, want repo escape rejection", err)
	}
}

func TestReadFileListRejectsSymlinkEscapingRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("make repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkPath := filepath.Join(repo, "linked.go")
	if err := os.Symlink(filepath.Join(root, "secret.go"), linkPath); err != nil {
		t.Skipf("symlinks are unavailable in this test environment: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("linked.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	_, _, err := Read(Config{}, Request{FileList: listPath, RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "escapes base directory") {
		t.Fatalf("Read error = %v, want symlink escape rejection", err)
	}
}

func TestReadFileListPreservesBlankLinesAndLineNumbers(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	filePath := filepath.Join(repo, "handler.go")
	if err := os.WriteFile(filePath, []byte("package handler\n\nfunc Serve() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	listPath := filepath.Join(repo, "files.txt")
	if err := os.WriteFile(listPath, []byte("handler.go\n"), 0o644); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	diff, _, err := Read(Config{}, Request{FileList: listPath, RepoPath: repo})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if !strings.Contains(string(diff), "@@ -0,0 +1,3 @@\n+package handler\n+\n+func Serve() {}\n") {
		t.Fatalf("synthetic diff must preserve blank lines and physical line count: %s", diff)
	}
}

func TestReadRepoPathInGitWorktreeSubdirectoryUsesGitDiff(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")

	subdir := filepath.Join(repo, "service")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("make subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "committed.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatalf("write committed source: %v", err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	diff, _, err := Read(Config{}, Request{RepoPath: subdir})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(diff) != 0 {
		t.Fatalf("committed files in a Git worktree subdirectory must not be synthesized as new: %s", diff)
	}
}

func TestReadRepoPathIncludesStagedUnstagedAndUntrackedChanges(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")

	if err := os.WriteFile(filepath.Join(repo, "staged.go"), []byte("package sample\n\nfunc staged() string { return \"old\" }\n"), 0o644); err != nil {
		t.Fatalf("write staged source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "unstaged.go"), []byte("package sample\n\nfunc unstaged() string { return \"old\" }\n"), 0o644); err != nil {
		t.Fatalf("write unstaged source: %v", err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repo, "staged.go"), []byte("package sample\n\nfunc staged() string { return \"new\" }\n"), 0o644); err != nil {
		t.Fatalf("rewrite staged source: %v", err)
	}
	git(t, repo, "add", "staged.go")

	if err := os.WriteFile(filepath.Join(repo, "unstaged.go"), []byte("package sample\n\nfunc unstaged() string { return \"new\" }\n"), 0o644); err != nil {
		t.Fatalf("rewrite unstaged source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package sample\n\nfunc untracked() string { return \"new\" }\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	diff, _, err := Read(Config{}, Request{RepoPath: repo})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	diffText := string(diff)
	for _, want := range []string{
		"diff --git a/staged.go b/staged.go",
		"diff --git a/unstaged.go b/unstaged.go",
		"diff --git a/untracked.go b/untracked.go",
	} {
		if !strings.Contains(diffText, want) {
			t.Fatalf("generated diff missing %q: %s", want, diffText)
		}
	}
}

func TestReadRepoPathRejectsUntrackedSymlink(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write tracked source: %v", err)
	}
	git(t, repo, "add", "tracked.go")
	git(t, repo, "commit", "-m", "initial")

	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package secret\n"), 0o644); err != nil {
		t.Fatalf("write outside source: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "untracked.go")); err != nil {
		t.Skipf("symlinks are unavailable in this test environment: %v", err)
	}

	_, _, err := Read(Config{}, Request{RepoPath: repo})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Read error = %v, want untracked symlink rejection", err)
	}
}

func TestGitDiffArgsDisableRepoConfiguredHelpers(t *testing.T) {
	t.Parallel()

	got := gitDiffArgs("/tmp/repo", "base", "head")
	want := []string{
		"-C", "/tmp/repo", "diff", "--no-ext-diff", "--no-textconv", "--unified=3", "base...head",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("git diff args = %#v, want %#v", got, want)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
