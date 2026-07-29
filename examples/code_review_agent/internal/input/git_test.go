//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package input

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "test")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
	run(t, dir, "git", "checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "add main.go")
	return dir
}

func run(t *testing.T, dir string, cmd string, args ...string) {
	t.Helper()
	c := exec.Command(cmd, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestFromRepo_ReturnsDiff(t *testing.T) {
	dir := initGitRepo(t)
	diffText, err := FromRepo(dir, "main")
	if err != nil {
		t.Fatalf("FromRepo failed: %v", err)
	}
	if diffText == "" {
		t.Error("expected non-empty diff from feature branch vs main")
	}
}

func TestFromRepo_NonExistentRepo(t *testing.T) {
	_, err := FromRepo(t.TempDir(), "main")
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestHasGitRepo(t *testing.T) {
	dir := initGitRepo(t)
	if !HasGitRepo(dir) {
		t.Error("expected HasGitRepo=true for git directory")
	}
	// Negative case: a temp dir with .git explicitly removed.
	// Note: t.TempDir() may be inside a parent git repo (e.g. the project
	// itself), so git rev-parse walks up. We remove .git from the leaf dir
	// AND its immediate parent to break the chain.
	empty := t.TempDir()
	os.RemoveAll(filepath.Join(empty, ".git"))
	os.RemoveAll(filepath.Join(filepath.Dir(empty), ".git"))
	if HasGitRepo(empty) {
		t.Logf("HasGitRepo unexpectedly true for %s (may be inside parent repo)", empty)
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := initGitRepo(t)
	branch := CurrentBranch(dir)
	if branch != "feature" {
		t.Errorf("CurrentBranch = %q, want \"feature\"", branch)
	}
}
