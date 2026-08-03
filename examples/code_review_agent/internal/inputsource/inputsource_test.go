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
	"reflect"
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
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
	if src.WorkDir != "" || src.RepoPath != "" {
		t.Fatalf("standalone diff unexpectedly has workspace: %#v", src)
	}
}

func TestReadDiffFileRejectsOversizeBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.diff")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	if _, err := readDiffFileWithLimit(path, "", 4); err == nil || !strings.Contains(err.Error(), "file exceeds 4 bytes") {
		t.Fatalf("readDiffFileWithLimit() error = %v, want size rejection", err)
	}
}

func TestReadDiffFileAssociatesRepositoryWorkspace(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(path, []byte("diff --git a/pkg/a.go b/pkg/a.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	src, err := Read(context.Background(), Options{DiffFile: path, RepoPath: repo})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantRepo, err := filepath.Abs(repo)
	if err != nil {
		t.Fatalf("Abs(repo) error = %v", err)
	}
	if src.RepoPath != wantRepo || src.WorkDir != wantRepo {
		t.Fatalf("workspace = %q/%q, want %q/%q", src.RepoPath, src.WorkDir, wantRepo, wantRepo)
	}
	if !strings.Contains(src.Summary, wantRepo) {
		t.Fatalf("Summary = %q, want repository path", src.Summary)
	}
}

func TestReadFixturesNormalizesLineEndings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.diff"), []byte("diff --git a/a.go b/a.go\r\n--- a/a.go\r\n+++ b/a.go\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(fixture) error = %v", err)
	}
	src, err := readFixtures(dir)
	if err != nil {
		t.Fatalf("readFixtures() error = %v", err)
	}
	if strings.Contains(src.Diff, "\r\n") {
		t.Fatalf("fixture diff was not normalized to LF: %q", src.Diff)
	}
}

func TestReadFixturesRejectsAggregateLimit(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"a.diff": "1234", "b.diff": "5678"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if _, err := readFixturesWithLimit(dir, 6); err == nil || !strings.Contains(err.Error(), "file exceeds") {
		t.Fatalf("readFixturesWithLimit() error = %v, want aggregate size rejection", err)
	}
}

func TestReadFixturesRejectsTooManyDiffFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.diff", "b.diff"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("diff --git a/a.go b/a.go\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if _, err := readFixturesWithLimits(dir, 1024, 1); err == nil || !strings.Contains(err.Error(), "fixture file count exceeded 1") {
		t.Fatalf("readFixturesWithLimits() error = %v, want file-count rejection", err)
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

func TestReadFileListPreservesPathWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(path, []byte(" selected.go \n  \n# comment\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	src, err := Read(context.Background(), Options{FileList: path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := []string{"  ", " selected.go "}
	if !reflect.DeepEqual(src.FileList, want) {
		t.Fatalf("FileList = %#v, want %#v", src.FileList, want)
	}
}

func TestReadFileListRejectsOversizeBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(path, []byte("123456"), 0o600); err != nil {
		t.Fatalf("WriteFile(file list) error = %v", err)
	}
	if _, err := readFileListWithLimit(path, "", 4); err == nil || !strings.Contains(err.Error(), "file exceeds 4 bytes") {
		t.Fatalf("readFileListWithLimit() error = %v, want size rejection", err)
	}
}

func TestReadFileListRejectsTooManyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(path, []byte("pkg/a.go\npkg/b.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(file list) error = %v", err)
	}
	if _, err := readFileListWithLimits(path, "", 1024, 1); err == nil || !strings.Contains(err.Error(), "file-list entry count exceeded 1") {
		t.Fatalf("readFileListWithLimits() error = %v, want file-count rejection", err)
	}
}

func TestReadDirectInputRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := readDiffFileWithLimit(dir, "", 1024); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("readDiffFileWithLimit() error = %v, want regular-file rejection", err)
	}
}

func TestReadFileListAssociatesRepositoryWorkspace(t *testing.T) {
	repo := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(path, []byte("pkg/my file.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	src, err := Read(context.Background(), Options{FileList: path, RepoPath: repo})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantRepo, err := filepath.Abs(repo)
	if err != nil {
		t.Fatalf("Abs(repo) error = %v", err)
	}
	if src.RepoPath != wantRepo || src.WorkDir != wantRepo {
		t.Fatalf("workspace = %q/%q, want %q/%q", src.RepoPath, src.WorkDir, wantRepo, wantRepo)
	}
	if len(src.FileList) != 1 || src.FileList[0] != "pkg/my file.go" {
		t.Fatalf("FileList = %#v, want exact path", src.FileList)
	}
	if !strings.Contains(src.Summary, wantRepo) {
		t.Fatalf("Summary = %q, want repository path", src.Summary)
	}
}

func TestUntrackedFileDiffQuotesAndPreservesSpecialPath(t *testing.T) {
	repo := t.TempDir()
	file := filepath.ToSlash(filepath.Join("pkg", "my file "+string([]byte{0xe6, 0x96, 0x87})+".go"))
	abs := filepath.Join(repo, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(abs, []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	diff, err := untrackedFileDiff(repo, file)
	if err != nil {
		t.Fatalf("untrackedFileDiff() error = %v", err)
	}
	if !strings.Contains(diff, "\"a/pkg/my file \\346\\226\\207.go\"") {
		t.Fatalf("generated diff did not use Git C-style quoting:\n%s", diff)
	}
	tabPath := "b/pkg/tab\t" + string([]byte{0xe6, 0x96, 0x87}) + ".go"
	if got, want := gitQuotePath(tabPath), "\"b/pkg/tab\\t\\346\\226\\207.go\""; got != want {
		t.Fatalf("gitQuotePath() = %q, want %q", got, want)
	}
	files, err := diffparse.Parse(diff)
	if err != nil {
		t.Fatalf("Parse(generated diff) error = %v", err)
	}
	if len(files) != 1 || files[0].NewPath != filepath.ToSlash(file) {
		t.Fatalf("parsed generated path = %#v, want %q", files, filepath.ToSlash(file))
	}
}

func TestReadRejectsMultipleInputSources(t *testing.T) {
	_, err := Read(context.Background(), Options{
		DiffFile: "a.diff",
		FileList: "files.txt",
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

func TestReadRepoDiffDisablesTextconv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("tracked.bin diff=sentinel\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.gitattributes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.bin"), []byte{0, 1}, 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.bin) error = %v", err)
	}
	runGit(t, dir, "add", ".gitattributes", "tracked.bin")
	runGit(t, dir, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "tracked.bin"), []byte{0, 2}, 0o600); err != nil {
		t.Fatalf("WriteFile(change) error = %v", err)
	}
	runGit(t, dir, "config", "diff.sentinel.textconv", "echo TEXTCONV_SENTINEL")

	src, err := Read(context.Background(), Options{RepoPath: dir})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if strings.Contains(src.Diff, "TEXTCONV_SENTINEL") {
		t.Fatalf("repo diff invoked configured textconv helper:\n%s", src.Diff)
	}
}

func TestReadRepoDiffDisablesConfiguredGitFilters(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("tracked.txt filter=sentinel\nincluded.txt filter=included\nworktree.txt filter=worktree\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.gitattributes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.txt) error = %v", err)
	}
	for _, name := range []string{"included.txt", "worktree.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("before\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	runGit(t, dir, "add", ".gitattributes", "tracked.txt", "included.txt", "worktree.txt")
	runGit(t, dir, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(change) error = %v", err)
	}
	for _, name := range []string{"included.txt", "worktree.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("after\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s change) error = %v", name, err)
		}
	}
	runGit(t, dir, "config", "filter.sentinel.clean", "echo FILTER_SENTINEL")
	includeConfig := filepath.Join(dir, ".git", "review-filter.inc")
	if err := os.WriteFile(includeConfig, []byte("[filter \"included\"]\n\tclean = echo INCLUDED_FILTER\n\tprocess = echo INCLUDED_FILTER\n\tsmudge = echo INCLUDED_FILTER\n\trequired = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(included filter config) error = %v", err)
	}
	runGit(t, dir, "config", "--local", "include.path", "review-filter.inc")
	runGit(t, dir, "config", "extensions.worktreeConfig", "true")
	runGit(t, dir, "config", "--worktree", "filter.worktree.clean", "echo WORKTREE_FILTER")
	runGit(t, dir, "config", "--worktree", "filter.worktree.process", "echo WORKTREE_FILTER")
	runGit(t, dir, "config", "--worktree", "filter.worktree.smudge", "echo WORKTREE_FILTER")

	options, err := gitReadOnlyOptions(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitReadOnlyOptions() error = %v", err)
	}
	optionText := strings.Join(options, " ")
	for _, want := range []string{
		"filter.sentinel.clean=",
		"filter.sentinel.process=",
		"filter.included.clean=",
		"filter.included.process=",
		"filter.worktree.clean=",
		"filter.worktree.process=",
	} {
		if !strings.Contains(optionText, want) {
			t.Fatalf("gitReadOnlyOptions() = %q, want %q", optionText, want)
		}
	}
	src, err := Read(context.Background(), Options{RepoPath: dir})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if strings.Contains(src.Diff, "FILTER_SENTINEL") {
		t.Fatalf("repo diff invoked configured clean filter:\n%s", src.Diff)
	}
	for _, sentinel := range []string{"INCLUDED_FILTER", "WORKTREE_FILTER"} {
		if strings.Contains(src.Diff, sentinel) {
			t.Fatalf("repo diff invoked configured %s helper:\n%s", sentinel, src.Diff)
		}
	}
}

func TestReadRepoDiffRejectsTrackedDiffOverLimit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	path := filepath.Join(dir, "tracked.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(base) error = %v", err)
	}
	runGit(t, dir, "add", "tracked.go")
	runGit(t, dir, "commit", "-m", "base")
	if err := os.WriteFile(path, []byte("package main\nconst value = \""+strings.Repeat("x", 256)+"\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(change) error = %v", err)
	}
	if _, err := readRepoDiffWithLimit(context.Background(), dir, 64); err == nil || !strings.Contains(err.Error(), "git output exceeded 64 bytes") {
		t.Fatalf("readRepoDiffWithLimit() error = %v, want tracked diff limit rejection", err)
	}
}

func TestUntrackedFileDiffRendersSymlinkWithoutReadingTarget(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatalf("Mkdir(repo) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("outside-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	linkTarget := filepath.ToSlash(filepath.Join("..", "outside.txt"))
	if err := os.Symlink(linkTarget, filepath.Join(repo, "leak.txt")); err != nil {
		t.Skipf("symlink creation is not supported in this environment: %v", err)
	}

	diff, err := untrackedFileDiff(repo, "leak.txt")
	if err != nil {
		t.Fatalf("untrackedFileDiff() error = %v", err)
	}
	if strings.Contains(diff, "outside-secret") {
		t.Fatalf("symlink target contents leaked into diff:\n%s", diff)
	}
	if !strings.Contains(diff, "new file mode 120000") {
		t.Fatalf("symlink diff did not use git symlink mode:\n%s", diff)
	}
	if !strings.Contains(diff, "+"+linkTarget) {
		t.Fatalf("symlink diff did not include link target %q:\n%s", linkTarget, diff)
	}
}

func TestUntrackedFileDiffWithLimitRejectsOversizeBeforeRead(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "large.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := untrackedFileDiffWithLimit(repo, "large.txt", 4); err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("untrackedFileDiffWithLimit() error = %v, want size rejection", err)
	}
}

func TestUntrackedFileDiffWithLimitBoundsGeneratedDiff(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "lines.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x\n", 32)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := untrackedFileDiffWithLimit(repo, "lines.txt", 128); err == nil || !strings.Contains(err.Error(), "diff for lines.txt exceeds 128 bytes") {
		t.Fatalf("untrackedFileDiffWithLimit() error = %v, want generated-diff limit rejection", err)
	}
}

func TestUntrackedFileDiffsRejectsTooManyFiles(t *testing.T) {
	paths := make([]string, maxUntrackedFileCount+1)
	for i := range paths {
		paths[i] = "file-" + strconv.Itoa(i)
	}
	_, err := untrackedFileDiffs(t.TempDir(), []byte(strings.Join(paths, "\x00")+"\x00"))
	if err == nil || !strings.Contains(err.Error(), "file count exceeded") {
		t.Fatalf("untrackedFileDiffs() error = %v, want file-count rejection", err)
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
