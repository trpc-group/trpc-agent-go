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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
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

func TestReadNonGitDirectoryIncludesNestedFiles(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	nested := filepath.Join(repo, "internal", "service")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("make nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "root.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write root source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "handler.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatalf("write nested source: %v", err)
	}

	diff, _, err := Read(Config{}, Request{RepoPath: repo})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	for _, want := range []string{
		"diff --git a/root.go b/root.go",
		"diff --git a/internal/service/handler.go b/internal/service/handler.go",
	} {
		if !strings.Contains(string(diff), want) {
			t.Fatalf("generated diff missing %q: %s", want, diff)
		}
	}
}

func TestReadNonGitDirectoryRejectsOversizedNestedInput(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	nested := filepath.Join(repo, "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("make nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "root.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write root source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "large.go"), []byte(strings.Repeat("x", 128)), 0o644); err != nil {
		t.Fatalf("write nested source: %v", err)
	}

	_, _, err := Read(Config{MaxInputBytes: 100}, Request{RepoPath: repo})
	if !errors.Is(err, errInputTooLarge) {
		t.Fatalf("Read error = %v, want input size limit rejection", err)
	}
}

func TestReadDiffFileRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	diffPath := filepath.Join(root, "change.diff")
	if err := os.WriteFile(diffPath, []byte("123456789"), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}

	_, _, err := Read(Config{MaxInputBytes: 8}, Request{DiffFile: diffPath})
	if !errors.Is(err, errInputTooLarge) {
		t.Fatalf("Read error = %v, want input size limit rejection", err)
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

func TestReadRepoPathInGitWorktreeSubdirectoryUsesRepoRelativeUntrackedPaths(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")

	subdir := filepath.Join(repo, "service")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("make subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatalf("write tracked source: %v", err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(subdir, "new.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	diff, _, err := Read(Config{}, Request{RepoPath: subdir})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	diffText := string(diff)
	if !strings.Contains(diffText, "diff --git a/service/new.go b/service/new.go") {
		t.Fatalf("generated diff missing repo-relative untracked path: %s", diffText)
	}
	if strings.Contains(diffText, "diff --git a/new.go b/new.go") {
		t.Fatalf("generated diff must not drop the subdirectory prefix: %s", diffText)
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

func TestReadRepoPathRejectsPartialGitRefs(t *testing.T) {
	repo := t.TempDir()
	for _, req := range []Request{
		{RepoPath: repo, BaseRef: "base"},
		{RepoPath: repo, HeadRef: "head"},
	} {
		_, _, err := Read(Config{}, req)
		if err == nil || !strings.Contains(err.Error(), "base ref and head ref must be supplied together") {
			t.Fatalf("Read(%+v) error = %v, want partial ref rejection", req, err)
		}
	}
}

func TestReadRepoPathRejectsOptionLikeGitRefWithoutDiffFile(t *testing.T) {
	repo := t.TempDir()

	_, _, err := Read(Config{}, Request{RepoPath: repo, BaseRef: "-main", HeadRef: "HEAD"})
	if err == nil || !strings.Contains(err.Error(), "must not start with '-'") {
		t.Fatalf("Read error = %v, want option-like git ref rejection", err)
	}
}

func TestReadRepoPathPropagatesGitDiscoveryErrors(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "service.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	sentinel := errors.New("git discovery exploded")
	withGitCommandOverride(t, repo, func(args []string, maxBytes int64, label string) ([]byte, error) {
		return nil, sentinel
	})

	_, _, err := Read(Config{}, Request{RepoPath: repo})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Read error = %v, want injected discovery error", err)
	}
}

func TestReadRepoPathRejectsOversizedUntrackedTotal(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")

	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write tracked source: %v", err)
	}
	git(t, repo, "add", "tracked.go")
	git(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repo, "one.go"), []byte("package sample\n\nfunc one() {}\n"), 0o644); err != nil {
		t.Fatalf("write first untracked source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "two.go"), []byte("package sample\n\nfunc two() {}\n"), 0o644); err != nil {
		t.Fatalf("write second untracked source: %v", err)
	}

	_, _, err := Read(Config{MaxInputBytes: 120}, Request{RepoPath: repo})
	if !errors.Is(err, errInputTooLarge) {
		t.Fatalf("Read error = %v, want input size limit rejection", err)
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

func TestReadRepoPathQuotesUntrackedNewlineFilename(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "reviewer@example.com")
	git(t, repo, "config", "user.name", "Review Agent Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write tracked source: %v", err)
	}
	git(t, repo, "add", "tracked.go")
	git(t, repo, "commit", "-m", "initial")

	name := "line\nbreak.go"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	diff, _, err := Read(Config{}, Request{RepoPath: repo})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if !strings.Contains(string(diff), "diff --git \"a/line\\nbreak.go\" \"b/line\\nbreak.go\"") {
		t.Fatalf("generated diff must quote newline filename: %s", diff)
	}
	parsed, err := review.ParseUnifiedDiff(string(diff))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != name {
		t.Fatalf("parsed files = %+v, want decoded path %q", parsed.Files, name)
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

func withGitCommandOverride(t *testing.T, repoPath string, runner gitCommandFunc) {
	t.Helper()
	gitCommandOverrides.Store(repoPath, runner)
	t.Cleanup(func() {
		gitCommandOverrides.Delete(repoPath)
	})
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
