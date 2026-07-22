//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package input

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

func TestParseConfigInputModes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{{"diff", []string{"--diff-file", "change.diff"}, false}, {"repo files", []string{"--repo-path", ".", "--files-file", "files.txt"}, false}, {"fixture", []string{"--fixture", "composite"}, false}, {"missing", nil, true}, {"conflict", []string{"--fixture", "composite", "--repo-path", "."}, true}, {"files without repo", []string{"--diff-file", "x", "--files-file", "files.txt"}, true}, {"local denied", []string{"--fixture", "composite", "--runtime", "local"}, true}, {"local explicit", []string{"--fixture", "composite", "--runtime", "local", "--allow-local"}, false}, {"unknown runtime", []string{"--fixture", "composite", "--runtime", "host"}, true}, {"unknown flag", []string{"--unknown"}, true}, {"positional", []string{"--fixture", "clean", "extra"}, true}, {"timeout", []string{"--fixture", "clean", "--timeout", "0s"}, true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseConfig(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseConfig() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
func TestValidFixtureName(t *testing.T) {
	for _, invalid := range []string{"", "../clean", "Clean", "a-b", "a/b"} {
		if validFixtureName(invalid) {
			t.Fatalf("validFixtureName(%q) = true", invalid)
		}
	}
	if !validFixtureName("goroutine_context") {
		t.Fatal("validFixtureName(goroutine_context) = false")
	}
}
func TestCollectGitDiffWithoutHEADAndExternalDiff(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "diff.external", "definitely-missing-code-review-command")
	writeTestFile(t, filepath.Join(root, "new.go"), []byte("package demo\n"))
	limits := Limits{1 << 20, 1 << 20, 10, 100, 1000}
	data, err := collectGitDiff(context.Background(), root, limits)
	if err != nil {
		t.Fatalf("collectGitDiff() error = %v", err)
	}
	files, err := diffparse.Parse(data)
	if err != nil || len(files) != 1 || files[0].NewPath != "new.go" {
		t.Fatalf("parsed files = %#v, error = %v", files, err)
	}
}
func TestCollectGitDiffLimitsUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	for _, name := range []string{"one.go", "two.go"} {
		writeTestFile(t, filepath.Join(root, name), []byte("package demo\n"))
	}
	limits := Limits{1 << 20, 1 << 20, 1, 100, 1000}
	_, err := collectGitDiff(context.Background(), root, limits)
	requireError(t, "collectGitDiff", err)
}
func TestCollectGitDiffUsesFinalWorktreeState(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "user.email", "review@example.com")
	runTestGit(t, root, "config", "user.name", "Review Test")
	path := filepath.Join(root, "state.go")
	initial := []byte("package demo\n\nconst state = \"safe\"\n")
	writeTestFile(t, path, initial)
	runTestGit(t, root, "add", "state.go")
	runTestGit(t, root, "commit", "--quiet", "-m", "initial")
	writeTestFile(t, path, []byte("package demo\n\nconst state = \"dangerous\"\n"))
	runTestGit(t, root, "add", "state.go")
	writeTestFile(t, path, initial)
	data, err := collectGitDiff(context.Background(), root, Limits{1 << 20, 1 << 20, 10, 100, 1000})
	if err != nil {
		t.Fatalf("collectGitDiff() error = %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("intermediate index state leaked into final diff: %s", data)
	}
}
func TestCollectUnbornDiffUsesWorktreeInsteadOfIndex(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	path := filepath.Join(root, "state.go")
	writeTestFile(t, path, []byte("package staged\n"))
	runTestGit(t, root, "add", "state.go")
	writeTestFile(t, path, []byte("package final\n"))
	data, err := collectGitDiff(context.Background(), root, Limits{1 << 20, 1 << 20, 10, 100, 1000})
	if err != nil {
		t.Fatalf("collectGitDiff() error = %v", err)
	}
	if string(data) == "" || !bytes.Contains(data, []byte("+package final")) || bytes.Contains(data, []byte("staged")) {
		t.Fatalf("unborn diff does not reflect final worktree: %s", data)
	}
}
func runTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	requireTestGit(t)
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = gitEnvironment()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func requireTestGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireError(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: error = nil", name)
	}
}

func testLimits() Limits {
	return Limits{MaxDiffBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxFiles: 20, MaxHunks: 100, MaxAdded: 1000}
}
func TestLoadDiffAndMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.diff")
	patch := "diff --git a/a.go b/a.go\n--- /dev/null\n+++ b/a.go\n@@ -0,0 +1 @@\n+package a\n"
	writeTestFile(t, path, []byte(patch))
	summary := mustLoad(t, Config{DiffFile: path, Limits: testLimits()})
	metadata := summary.Metadata()
	if metadata.Kind != "diff" || metadata.FileCount != 1 || metadata.AddedLines != 1 || metadata.Digest == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
func TestLoadFilesMode(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), []byte("package a\n"))
	list := filepath.Join(t.TempDir(), "files.txt")
	writeTestFile(t, list, []byte("a.go\n"))
	summary := mustLoad(t, Config{RepoPath: root, FilesFile: list, Limits: testLimits()})
	if summary.Kind != "files" || len(summary.Packages) != 1 {
		t.Fatalf("summary = %#v", summary.Metadata())
	}
}
func TestLoadRepoMode(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, "a.go"), []byte("package a\n"))
	summary := mustLoad(t, Config{RepoPath: root, Limits: testLimits()})
	if summary.Kind != "repo" || len(summary.Files) != 1 {
		t.Fatalf("summary = %#v", summary.Metadata())
	}
}
func mustLoad(t *testing.T, config Config) Summary {
	t.Helper()
	summary, err := Load(context.Background(), config)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return summary
}
func TestLoadRejectsLimitsAndMalformed(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "large.diff")
	writeTestFile(t, large, []byte("too large"))
	limits := testLimits()
	limits.MaxDiffBytes = 2
	_, err := Load(context.Background(), Config{DiffFile: large, Limits: limits})
	requireError(t, "oversize diff", err)
	malformed := filepath.Join(dir, "malformed.diff")
	writeTestFile(t, malformed, []byte("not a diff"))
	_, err = Load(context.Background(), Config{DiffFile: malformed, Limits: testLimits()})
	requireError(t, "malformed diff", err)
}
func TestLoadFixtureAndInvalidRepo(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	summary, err := Load(context.Background(), Config{Fixture: "composite", Limits: testLimits()})
	if err != nil || summary.Kind != "fixture" {
		t.Fatalf("summary=%#v error=%v", summary.Metadata(), err)
	}
	if _, err := Load(context.Background(), Config{RepoPath: filepath.Join(root, "missing"), Limits: testLimits()}); err == nil {
		t.Fatal("missing repo accepted")
	}
}
func TestNewFilesDiff(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   []byte
		binary bool
	}{{"text", []byte("package demo\n"), false}, {"binary", []byte{0, 1, 2}, true}} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			name := test.name + ".go"
			writeTestFile(t, filepath.Join(root, name), test.data)
			data, err := newFilesDiff(root, []string{name}, Limits{1024, 1024, 2, 10, 10})
			if err != nil {
				t.Fatalf("newFilesDiff() error = %v", err)
			}
			files, err := diffparse.Parse(data)
			if err != nil || len(files) != 1 || files[0].NewPath != name || files[0].Binary != test.binary {
				t.Fatalf("parsed files = %#v, error = %v", files, err)
			}
		})
	}
}
func TestPureHelpers(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{{"", 0}, {"\n", 1}, {"one\n", 1}, {"one\ntwo", 2}}
	for _, test := range tests {
		if got := len(splitFileLines([]byte(test.input))); got != test.want {
			t.Fatalf("splitFileLines(%q) = %d, want %d", test.input, got, test.want)
		}
	}
	if got := gitDiffPath("b/", "weird\n\"name.go"); got != `"b/weird\n\"name.go"` {
		t.Fatalf("gitDiffPath() = %q", got)
	}
	files := []diffparse.ChangedFile{{NewPath: "z/z.go"}, {NewPath: "a/a.go"}, {NewPath: "README.md"}, {OldPath: "a/old.go"}}
	got := ResolvePackages("repo", files)
	if len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("ResolvePackages() = %v", got)
	}
	var buffer boundedBuffer
	buffer.limit = 3
	n, err := buffer.Write([]byte("abcdef"))
	if err != nil || n != 6 || buffer.String() != "abc" || !buffer.truncated {
		t.Fatalf("Write() = %d, %v; buffer = %q, truncated = %v", n, err, buffer.String(), buffer.truncated)
	}
}
func TestCleanRelativePathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	writeTestFile(t, outside, []byte("package outside"))
	t.Cleanup(func() {
		if err := os.Remove(outside); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove outside fixture: %v", err)
		}
	})
	for name, path := range map[string]string{"escape": "../outside.go", "absolute": filepath.Join(root, "x.go")} {
		_, err := cleanRelativePath(root, path)
		requireError(t, name, err)
	}
}

func TestInputErrorAndBoundaryBranches(t *testing.T) {
	root := t.TempDir()
	_, err := readFileList(root, filepath.Join(root, "missing"), testLimits())
	requireError(t, "missing file list", err)
	filePath := filepath.Join(root, "a.go")
	writeTestFile(t, filePath, []byte("package a\n"))
	listPath := filepath.Join(root, "files.txt")
	writeTestFile(t, listPath, []byte("\na.go\na.go\n"))
	limits := testLimits()
	limits.MaxFiles = 1
	_, err = readFileList(root, listPath, limits)
	requireError(t, "oversize file list", err)
	longList := filepath.Join(root, "long.txt")
	writeTestFile(t, longList, []byte(strings.Repeat("a", maxFileListLineBytes+1)))
	_, err = readFileList(root, longList, testLimits())
	requireError(t, "oversize file-list line", err)
	writeTestFile(t, listPath, []byte("../outside.go\n"))
	_, err = readFileList(root, listPath, testLimits())
	requireError(t, "escaping file-list path", err)
	_, err = newFilesDiff(root, []string{"missing.go"}, testLimits())
	requireError(t, "missing listed file", err)
	limits = testLimits()
	limits.MaxFileBytes = 1
	_, err = newFilesDiff(root, []string{"a.go"}, limits)
	requireError(t, "oversize listed file", err)
	limits = testLimits()
	limits.MaxDiffBytes = 1
	_, err = newFilesDiff(root, []string{"a.go"}, limits)
	requireError(t, "oversize generated diff", err)
	binaryPath := filepath.Join(root, "image.bin")
	writeTestFile(t, binaryPath, []byte{0, 1})
	_, err = newFilesDiff(root, []string{"image.bin"}, limits)
	requireError(t, "oversize generated binary diff", err)
	_, err = runGit(context.Background(), root, 0, "status")
	requireError(t, "non-positive git output limit", err)
	_, err = runGit(context.Background(), root, 1024, "definitely-not-a-git-subcommand")
	requireError(t, "invalid git subcommand", err)
	_, err = collectGitDiff(context.Background(), root, testLimits())
	requireError(t, "non-git directory", err)
	runTestGit(t, root, "init", "--quiet")
	err = appendListedFiles(root, testLimits(), &bytes.Buffer{}, []byte(filepath.Clean(filePath)+"\x00"))
	requireError(t, "absolute listed path", err)
	if sources, err := loadChangedSources("", nil, testLimits()); err != nil || sources != nil {
		t.Fatalf("empty source root = %#v, %v", sources, err)
	}
	skipped := []diffparse.ChangedFile{{NewPath: "deleted.go", Deleted: true}, {NewPath: "binary.go", Binary: true}, {NewPath: "README.md"}}
	if sources, err := loadChangedSources(root, skipped, testLimits()); err != nil || len(sources) != 0 {
		t.Fatalf("skipped sources = %#v, %v", sources, err)
	}
	_, err = loadChangedSources(root, []diffparse.ChangedFile{{NewPath: "missing.go"}}, testLimits())
	requireError(t, "missing changed source", err)
	limits = testLimits()
	limits.MaxDiffBytes = 1
	_, err = loadChangedSources(root, []diffparse.ChangedFile{{NewPath: "a.go"}}, limits)
	requireError(t, "oversize changed source aggregate", err)
	_, err = secureRepoRoot(filePath)
	requireError(t, "regular file repo root", err)
	for name, path := range map[string]string{"empty": "", "NUL": "bad\x00path", "directory": "."} {
		_, err = cleanRelativePath(root, path)
		requireError(t, name+" relative path", err)
	}
	if packages := ResolvePackages("", []diffparse.ChangedFile{{NewPath: "a.go"}}); packages != nil {
		t.Fatalf("packages without root = %v", packages)
	}
	_, err = Load(context.Background(), Config{Fixture: "../bad", Limits: testLimits()})
	requireError(t, "unsafe fixture name", err)
	patchPath := filepath.Join(root, "single.diff")
	patch := "diff --git a/a.go b/a.go\n--- /dev/null\n+++ b/a.go\n@@ -0,0 +1 @@\n+package a\n"
	writeTestFile(t, patchPath, []byte(patch))
	limits = testLimits()
	limits.MaxFiles = 0
	_, err = Load(context.Background(), Config{DiffFile: patchPath, Limits: limits})
	requireError(t, "input file-count limit", err)
}
