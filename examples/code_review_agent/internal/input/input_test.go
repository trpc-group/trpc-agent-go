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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

func TestVerifyRawUnchangedDetectsHeadMove(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "user.email", "review@example.com")
	runTestGit(t, root, "config", "user.name", "Review Test")
	path := filepath.Join(root, "state.go")
	writeTestFile(t, path, []byte("package demo\n\nconst state = \"safe\"\n"))
	runTestGit(t, root, "add", "state.go")
	runTestGit(t, root, "commit", "--quiet", "-m", "initial")
	writeTestFile(t, path, []byte("package demo\n\nconst state = \"new\"\n"))
	runTestGit(t, root, "add", "state.go")
	runTestGit(t, root, "commit", "--quiet", "-m", "second")
	config := Config{
		RepoPath: root,
		Limits:   Limits{1 << 20, 1 << 20, 10, 100, 1000},
	}
	kind, repoRoot, raw, err := loadRaw(ctx, config, nil)
	if err != nil || len(raw) != 0 {
		t.Fatalf("loadRaw() kind=%q root=%q raw=%q error=%v",
			kind, repoRoot, raw, err)
	}
	runTestGit(t, root, "reset", "--soft", "HEAD~1")
	if err := verifyRawUnchanged(
		ctx,
		config,
		nil,
		kind,
		repoRoot,
		raw,
	); err == nil || !strings.Contains(err.Error(), "review diff changed") {
		t.Fatalf("verifyRawUnchanged() error = %v", err)
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
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*", "-C", root}, args...)...)
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

func TestCollectFilesDiffFiltersTrackedPaths(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "user.email", "review@example.com")
	runTestGit(t, root, "config", "user.name", "Review Test")
	magic := "[literal].go"
	writeTestFile(
		t,
		filepath.Join(root, magic),
		[]byte("package demo\n\nconst old = 1\n"),
	)
	writeTestFile(
		t,
		filepath.Join(root, "unchanged.go"),
		[]byte("package demo\n"),
	)
	writeTestFile(
		t,
		filepath.Join(root, ".gitignore"),
		[]byte("ignored.go\n"),
	)
	runTestGit(
		t,
		root,
		"--literal-pathspecs",
		"add",
		"--",
		magic,
		"unchanged.go",
		".gitignore",
	)
	runTestGit(t, root, "commit", "--quiet", "-m", "initial")
	writeTestFile(
		t,
		filepath.Join(root, magic),
		[]byte("package demo\n\nconst old = 1\nconst added = 2\n"),
	)
	writeTestFile(
		t,
		filepath.Join(root, "untracked.go"),
		[]byte("package demo\n"),
	)
	writeTestFile(
		t,
		filepath.Join(root, "ignored.go"),
		[]byte("package ignored\n"),
	)

	selected := []string{
		"untracked.go",
		"unchanged.go",
		"ignored.go",
		magic,
		magic,
	}
	data, err := collectFilesDiff(
		context.Background(),
		root,
		selected,
		testLimits(),
	)
	if err != nil {
		t.Fatalf("collectFilesDiff() error = %v", err)
	}
	second, err := collectFilesDiff(
		context.Background(),
		root,
		selected,
		testLimits(),
	)
	if err != nil || !bytes.Equal(data, second) {
		t.Fatalf("collectFilesDiff() unstable: error=%v", err)
	}
	raw := string(data)
	for _, expected := range []string{
		"diff --git a/[literal].go b/[literal].go",
		"--- a/[literal].go",
		"+const added = 2",
		"diff --git a/untracked.go b/untracked.go",
		"diff --git a/ignored.go b/ignored.go",
		"--- /dev/null",
	} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("diff missing %q:\n%s", expected, raw)
		}
	}
	for _, unexpected := range []string{
		"unchanged.go",
		"+const old = 1",
	} {
		if strings.Contains(raw, unexpected) {
			t.Fatalf("diff unexpectedly contains %q:\n%s", unexpected, raw)
		}
	}
	if strings.Index(raw, magic) > strings.Index(raw, "untracked.go") {
		t.Fatalf("tracked diff must precede untracked additions:\n%s", raw)
	}
	limited := testLimits()
	limited.MaxDiffBytes = int64(len(data) - 1)
	if _, err := collectFilesDiff(
		context.Background(),
		root,
		selected,
		limited,
	); err == nil || !strings.Contains(err.Error(), "diff exceeds") {
		t.Fatalf("combined diff limit error = %v", err)
	}
}

func TestCollectFilesDiffUsesFinalTrackedWorktree(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "user.email", "review@example.com")
	runTestGit(t, root, "config", "user.name", "Review Test")
	path := filepath.Join(root, "state.go")
	initial := []byte("package demo\n\nconst state = \"safe\"\n")
	writeTestFile(t, path, initial)
	runTestGit(t, root, "add", "state.go")
	runTestGit(t, root, "commit", "--quiet", "-m", "initial")
	writeTestFile(
		t,
		path,
		[]byte("package demo\n\nconst state = \"staged\"\n"),
	)
	runTestGit(t, root, "add", "state.go")
	writeTestFile(t, path, initial)

	data, err := collectFilesDiff(
		context.Background(),
		root,
		[]string{"state.go"},
		testLimits(),
	)
	if err != nil {
		t.Fatalf("collectFilesDiff() error = %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("intermediate index state leaked into diff:\n%s", data)
	}
}

func TestCollectFilesDiffFallsBackWithoutHead(t *testing.T) {
	for _, test := range []struct {
		name    string
		initGit bool
	}{
		{name: "non git"},
		{name: "unborn head", initGit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.initGit {
				runTestGit(t, root, "init", "--quiet")
			}
			writeTestFile(
				t,
				filepath.Join(root, "selected.go"),
				[]byte("package final\n"),
			)
			if test.initGit {
				runTestGit(t, root, "add", "selected.go")
			}
			data, err := collectFilesDiff(
				context.Background(),
				root,
				[]string{"selected.go"},
				testLimits(),
			)
			if err != nil {
				t.Fatalf("collectFilesDiff() error = %v", err)
			}
			if !bytes.Contains(data, []byte("--- /dev/null")) ||
				!bytes.Contains(data, []byte("+package final")) {
				t.Fatalf("fallback diff = %s", data)
			}
		})
	}
}

func TestCollectFilesDiffPropagatesGitProbeFailures(t *testing.T) {
	requireTestGit(t)
	t.Run("canceled context", func(t *testing.T) {
		root := t.TempDir()
		runTestGit(t, root, "init", "--quiet")
		writeTestFile(t, filepath.Join(root, "selected.go"), []byte("package demo\n"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := collectFilesDiff(ctx, root, []string{"selected.go"}, testLimits())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collectFilesDiff() error = %v, want context.Canceled", err)
		}
	})
	t.Run("invalid head", func(t *testing.T) {
		root := t.TempDir()
		runTestGit(t, root, "init", "--quiet")
		writeTestFile(t, filepath.Join(root, "selected.go"), []byte("package demo\n"))
		writeTestFile(t, filepath.Join(root, ".git", "HEAD"), []byte("invalid head\n"))
		if _, err := collectFilesDiff(
			context.Background(),
			root,
			[]string{"selected.go"},
			testLimits(),
		); err == nil {
			t.Fatal("collectFilesDiff() error = nil, want invalid HEAD error")
		}
	})
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
	hints := ResolveModuleHints([]diffparse.ChangedFile{
		{NewPath: "nested/go.mod"},
		{NewPath: "other/go.sum"},
		{NewPath: "pkg/value.go"},
		{OldPath: "oldmod/value.go", NewPath: "newmod/value.go"},
		{NewPath: "nested/migrations/001.sql"},
		{NewPath: "README.md"},
	})
	if !slices.Equal(hints, []string{".", "nested", "nested/migrations", "newmod", "oldmod", "other", "pkg"}) {
		t.Fatalf("ResolveModuleHints() = %v", hints)
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
	writeTestFile(t, filepath.Join(root, ".git"), []byte("not a git directory\n"))
	_, err = collectGitDiff(context.Background(), root, testLimits())
	requireError(t, "non-git directory", err)
	if err := os.Remove(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
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

func TestLoadUsesImmutableRepositorySnapshot(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summary := mustLoad(t, Config{RepoPath: root, Limits: testLimits()})
	if summary.RepoRoot == root {
		t.Fatal("Load returned the mutable source repository")
	}
	if summary.RepositoryDigest == "" {
		t.Fatal("Load returned an empty repository digest")
	}
	if summary.Cleanup == nil {
		t.Fatal("Load returned no snapshot cleanup")
	}
	defer assertSnapshotCleanup(t, summary.RepoRoot, summary.Cleanup)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(summary.RepoRoot, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package main\n" {
		t.Fatalf("snapshot content = %q", got)
	}
	info, err := os.Stat(filepath.Join(summary.RepoRoot, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("snapshot file mode = %04o, want read-only", info.Mode().Perm())
	}
}

func TestSnapshotAllowsInternalFileSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	if err := os.WriteFile(target, []byte("package linked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.go", filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	snapshot, _, cleanup, err := snapshotRepository(root, Limits{MaxFileBytes: 1 << 20, MaxFiles: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer assertSnapshotCleanup(t, snapshot, cleanup)
	data, err := os.ReadFile(filepath.Join(snapshot, "linked.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package linked\n" {
		t.Fatalf("linked snapshot content = %q", data)
	}
}

func TestSnapshotAndDigestExcludeOnlyGitMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prefixedDirectory := filepath.Join(root, "a")
	if err := os.Mkdir(prefixedDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefixedDirectory, "b.go"), []byte("package review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, ".git-hooks")
	if err := os.Mkdir(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "hook"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceDigest, err := DigestRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, snapshotDigest, cleanup, err := snapshotRepository(root, Limits{MaxFileBytes: 1 << 20, MaxFiles: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer assertSnapshotCleanup(t, snapshot, cleanup)
	if sourceDigest != snapshotDigest {
		t.Fatalf("source digest %q != snapshot digest %q", sourceDigest, snapshotDigest)
	}
	digestFromSnapshot, err := DigestRepository(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotDigest != digestFromSnapshot {
		t.Fatalf("returned digest %q != digest from snapshot %q", snapshotDigest, digestFromSnapshot)
	}
	if _, err := os.Stat(filepath.Join(snapshot, ".git-hooks", "hook")); err != nil {
		t.Fatalf("snapshot tracked .git-* directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, ".git")); !os.IsNotExist(err) {
		t.Fatalf("snapshot contains .git file: %v", err)
	}
}

func TestLoadRepoSnapshotUsesGitInventory(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, ".gitignore"), []byte(".env\n"))
	writeTestFile(t, filepath.Join(root, "tracked.go"), []byte("package review\n"))
	writeTestFile(t, filepath.Join(root, "untracked.go"), []byte("package review\n"))
	writeTestFile(t, filepath.Join(root, ".env"), []byte("arbitrary-secret-one\n"))
	runTestGit(t, root, "add", ".gitignore", "tracked.go")

	first := mustLoad(t, Config{RepoPath: root, Limits: testLimits()})
	defer assertSnapshotCleanup(t, first.RepoRoot, first.Cleanup)
	for _, name := range []string{".gitignore", "tracked.go", "untracked.go"} {
		if _, err := os.Stat(filepath.Join(first.RepoRoot, name)); err != nil {
			t.Fatalf("snapshot missing %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(first.RepoRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("snapshot contains ignored file: %v", err)
	}

	writeTestFile(t, filepath.Join(root, ".env"), []byte("arbitrary-secret-two\n"))
	second := mustLoad(t, Config{RepoPath: root, Limits: testLimits()})
	defer assertSnapshotCleanup(t, second.RepoRoot, second.Cleanup)
	if first.RepositoryDigest != second.RepositoryDigest {
		t.Fatalf("ignored file changed repository digest: %q != %q", first.RepositoryDigest, second.RepositoryDigest)
	}
}

func TestLoadFilesSnapshotCanOptInIgnoredFile(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, ".gitignore"), []byte(".env\n"))
	writeTestFile(t, filepath.Join(root, ".env"), []byte("explicit input\n"))
	list := filepath.Join(t.TempDir(), "files.txt")
	writeTestFile(t, list, []byte(".env\n"))

	summary := mustLoad(t, Config{RepoPath: root, FilesFile: list, Limits: testLimits()})
	defer assertSnapshotCleanup(t, summary.RepoRoot, summary.Cleanup)
	if _, err := os.Stat(filepath.Join(summary.RepoRoot, ".env")); err != nil {
		t.Fatalf("explicit ignored file missing from snapshot: %v", err)
	}
}

func TestRepositoryInventoryUsesGitModesAndSkipsGitlinks(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, "helper.sh"), []byte("#!/bin/sh\nexit 0\n"))
	runTestGit(t, root, "add", "helper.sh")
	runTestGit(t, root, "update-index", "--chmod=+x", "helper.sh")
	runTestGit(t, root, "update-index", "--add", "--cacheinfo",
		"160000,1111111111111111111111111111111111111111,vendor/submodule")

	inventory, err := repositoryInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 1 || inventory[0].Path != "helper.sh" || !inventory[0].Executable {
		t.Fatalf("inventory = %#v", inventory)
	}
	summary := mustLoad(t, Config{RepoPath: root, Limits: testLimits()})
	defer assertSnapshotCleanup(t, summary.RepoRoot, summary.Cleanup)
	if !slices.Equal(summary.ExecutablePaths, []string{"helper.sh"}) {
		t.Fatalf("executable paths = %v", summary.ExecutablePaths)
	}
}

func TestRepositoryInventoryRejectsUnmergedIndex(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	hashes := make([]string, 3)
	for index, content := range []string{"base\n", "ours\n", "theirs\n"} {
		name := fmt.Sprintf("blob-%d", index)
		writeTestFile(t, filepath.Join(root, name), []byte(content))
		cmd := exec.Command(
			"git",
			"-c",
			"safe.directory=*",
			"-C",
			root,
			"hash-object",
			"-w",
			name,
		)
		cmd.Env = gitEnvironment()
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git hash-object: %v: %s", err, output)
		}
		hashes[index] = strings.TrimSpace(string(output))
	}
	cmd := exec.Command(
		"git",
		"-c",
		"safe.directory=*",
		"-C",
		root,
		"update-index",
		"--index-info",
	)
	cmd.Env = gitEnvironment()
	cmd.Stdin = strings.NewReader(fmt.Sprintf(
		"100644 %s 1\tconflict.go\n"+
			"100644 %s 2\tconflict.go\n"+
			"100755 %s 3\tconflict.go\n",
		hashes[0],
		hashes[1],
		hashes[2],
	))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git update-index: %v: %s", err, output)
	}
	if _, err := repositoryInventory(context.Background(), root); err == nil ||
		!strings.Contains(err.Error(), "unresolved merge") {
		t.Fatalf("repositoryInventory() error = %v", err)
	}
}

func TestLoadRejectsSymlinkToIgnoredFile(t *testing.T) {
	requireTestGit(t)
	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, ".gitignore"), []byte(".env\n"))
	writeTestFile(t, filepath.Join(root, ".env"), []byte("arbitrary credential\n"))
	if err := os.Symlink(".env", filepath.Join(root, "reviewed.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	runTestGit(t, root, "add", ".gitignore", "reviewed.go")
	if _, err := Load(context.Background(), Config{RepoPath: root, Limits: testLimits()}); err == nil ||
		!strings.Contains(err.Error(), "outside snapshot inventory") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestReadSnapshotFileRejectsChangedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changing.go")
	writeTestFile(t, path, []byte("package review\n"))
	expected, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package review\nvar changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSnapshotFile(path, expected, 1<<20, "changing.go"); err == nil ||
		!strings.Contains(err.Error(), "changed before read") {
		t.Fatalf("readSnapshotFile() error = %v", err)
	}
}

func TestSnapshotFileLimitIsIndependentFromDiffLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package review\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _, cleanup, err := snapshotRepository(root, Limits{MaxFileBytes: 1 << 20, MaxFiles: 1})
	if err != nil {
		t.Fatalf("snapshotRepository() error = %v", err)
	}
	defer assertSnapshotCleanup(t, snapshot, cleanup)
}

func assertSnapshotCleanup(t *testing.T, snapshot string, cleanup func() error) {
	t.Helper()
	if err := cleanup(); err != nil {
		t.Errorf("snapshot cleanup error = %v", err)
		return
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Errorf("snapshot remains after cleanup: %v", err)
	}
}
