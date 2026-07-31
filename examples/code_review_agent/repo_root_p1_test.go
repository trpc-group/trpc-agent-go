//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadReviewInputResolvesWorktreeRootBeforeDiff(t *testing.T) {
	repoRoot := t.TempDir()
	nestedPath := filepath.Join(repoRoot, "nested", "work")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := resolveExistingPath(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	type gitCall struct {
		ctx      context.Context
		repoPath string
		args     []string
	}
	var calls []gitCall
	runner := func(
		ctx context.Context,
		repoPath string,
		args []string,
	) ([]byte, []byte, error) {
		calls = append(calls, gitCall{
			ctx:      ctx,
			repoPath: repoPath,
			args:     append([]string(nil), args...),
		})
		switch len(calls) {
		case 1:
			return []byte(filepath.ToSlash(repoRoot) + "\n"), nil, nil
		case 2:
			return []byte(repoRootP1MinimalDiff("pkg/review.go")), nil, nil
		default:
			t.Fatalf("unexpected git call %d", len(calls))
			return nil, nil, nil
		}
	}

	input, err := loadReviewInput(context.Background(), config{
		repoPath: nestedPath,
		files:    repeatedStrings{"pkg/review.go"},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("git calls = %d, want 2", len(calls))
	}
	if calls[0].ctx != calls[1].ctx {
		t.Fatal("rev-parse and diff did not share one timeout context")
	}
	if _, ok := calls[0].ctx.Deadline(); !ok {
		t.Fatal("git calls did not receive a deadline")
	}
	if calls[0].repoPath != nestedPath {
		t.Fatalf("rev-parse cwd = %q, want %q", calls[0].repoPath, nestedPath)
	}
	if want := []string{"rev-parse", "--show-toplevel"}; !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("rev-parse args = %#v, want %#v", calls[0].args, want)
	}
	if calls[1].repoPath != resolvedRoot {
		t.Fatalf("diff cwd = %q, want canonical root %q", calls[1].repoPath, resolvedRoot)
	}
	wantDiffArgs := []string{
		"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--", "pkg/review.go",
	}
	if !reflect.DeepEqual(calls[1].args, wantDiffArgs) {
		t.Fatalf("diff args = %#v, want %#v", calls[1].args, wantDiffArgs)
	}
	if input.source != nestedPath {
		t.Fatalf("source = %q, want original input %q", input.source, nestedPath)
	}
	if input.repoRoot != resolvedRoot {
		t.Fatalf("repo root = %q, want %q", input.repoRoot, resolvedRoot)
	}
	if !reflect.DeepEqual(input.repoFiles, []string{"pkg/review.go"}) {
		t.Fatalf("repo files = %#v", input.repoFiles)
	}
}

func TestLoadReviewInputRejectsInvalidWorktreeRootWithoutDiff(t *testing.T) {
	inputPath := t.TempDir()
	outsideRoot := t.TempDir()
	fileRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(fileRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingRoot := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name      string
		stdout    []byte
		stderr    []byte
		runnerErr error
		wantErr   string
	}{
		{
			name:      "command failure",
			stderr:    []byte("fatal: unavailable\n"),
			runnerErr: errors.New("exit status 1"),
			wantErr:   "resolve git worktree root: exit status 1: fatal: unavailable",
		},
		{name: "empty", wantErr: "empty path"},
		{
			name:    "multiple lines",
			stdout:  []byte(filepath.ToSlash(inputPath) + "\n" + filepath.ToSlash(outsideRoot) + "\n"),
			wantErr: "multiple path lines",
		},
		{
			name:    "NUL",
			stdout:  append([]byte(filepath.ToSlash(inputPath)), 0, '\n'),
			wantErr: "contains a NUL byte",
		},
		{name: "relative", stdout: []byte("relative/root\n"), wantErr: "non-absolute path"},
		{
			name:    "outside input",
			stdout:  []byte(filepath.ToSlash(outsideRoot) + "\n"),
			wantErr: "outside the resolved git worktree root",
		},
		{
			name:    "not a directory",
			stdout:  []byte(filepath.ToSlash(fileRoot) + "\n"),
			wantErr: "git worktree root is not a directory",
		},
		{
			name:    "missing directory",
			stdout:  []byte(filepath.ToSlash(missingRoot) + "\n"),
			wantErr: "resolve git worktree root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			runner := func(
				_ context.Context,
				repoPath string,
				args []string,
			) ([]byte, []byte, error) {
				calls++
				if calls != 1 {
					t.Fatalf("diff ran after invalid rev-parse output: call %d", calls)
				}
				if repoPath != inputPath {
					t.Fatalf("rev-parse cwd = %q, want %q", repoPath, inputPath)
				}
				if want := []string{"rev-parse", "--show-toplevel"}; !reflect.DeepEqual(args, want) {
					t.Fatalf("rev-parse args = %#v, want %#v", args, want)
				}
				return tt.stdout, tt.stderr, tt.runnerErr
			}

			_, err := loadReviewInput(context.Background(), config{repoPath: inputPath}, runner)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadReviewInput error = %v, want substring %q", err, tt.wantErr)
			}
			if calls != 1 {
				t.Fatalf("git calls = %d, want only rev-parse", calls)
			}
		})
	}
}

func TestLoadReviewInputNestedRepoUsesRootRelativeFileFilter(t *testing.T) {
	repoRoot := t.TempDir()
	repoRootP1RunGit(t, repoRoot, "init")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "pkg", "review.go"), "package pkg\n\nconst Value = 1\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "other", "other.go"), "package other\n\nconst Other = 1\n")
	repoRootP1RunGit(t, repoRoot, "add", "go.mod", "pkg/review.go", "other/other.go")
	repoRootP1Commit(t, repoRoot)

	repoRootP1WriteFile(t, filepath.Join(repoRoot, "pkg", "review.go"), "package pkg\n\nconst Value = 2\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "other", "other.go"), "package other\n\nconst Other = 2\n")
	nestedPath := filepath.Join(repoRoot, "pkg", "nested")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	input, err := loadReviewInput(context.Background(), config{
		repoPath: nestedPath,
		files:    repeatedStrings{"pkg/review.go"},
	}, runGitCommand)
	if err != nil {
		t.Fatal(err)
	}
	if input.source != nestedPath {
		t.Fatalf("input source = %q, want original path %q", input.source, nestedPath)
	}
	wantRootInfo, err := os.Stat(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	gotRootInfo, err := os.Stat(input.repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(input.repoRoot) || !os.SameFile(wantRootInfo, gotRootInfo) {
		t.Fatalf("input repo root = %q, want canonical path for %q", input.repoRoot, repoRoot)
	}
	diff := string(input.diff)
	if !strings.Contains(diff, "diff --git a/pkg/review.go b/pkg/review.go") {
		t.Fatalf("root-relative filtered diff did not include pkg/review.go:\n%s", diff)
	}
	if strings.Contains(diff, "other/other.go") {
		t.Fatalf("root-relative filtered diff included other/other.go:\n%s", diff)
	}
}

func TestNestedRepoRootSupportsASTSnapshotAndModuleManifest(t *testing.T) {
	repoRoot := t.TempDir()
	repoRootP1RunGit(t, repoRoot, "init")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "pkg", "review.go"), "package pkg\n")
	repoRootP1RunGit(t, repoRoot, "add", "go.mod", "pkg/review.go")
	repoRootP1Commit(t, repoRoot)

	repoRootP1WriteFile(t, filepath.Join(repoRoot, "pkg", "review.go"), strings.Join([]string{
		"package pkg",
		"",
		`import "os"`,
		"",
		"func Read() error {",
		"\tf, err := os.Open(\"review.txt\")",
		"\tif err != nil {",
		"\t\treturn err",
		"\t}",
		"\tdefer f.Close()",
		"\treturn nil",
		"}",
		"",
	}, "\n"))
	nestedPath := filepath.Join(repoRoot, "pkg", "nested")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	input, err := loadReviewInput(
		context.Background(),
		config{repoPath: nestedPath},
		runGitCommand,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseUnifiedDiff(input.diff)
	if len(parsed.Files) != 1 || parsed.Files[0].reviewPath() != "pkg/review.go" {
		t.Fatalf("parsed files = %+v", parsed.Files)
	}
	foundOpenCandidate := false
	for _, candidate := range parsed.candidateLines() {
		if strings.Contains(candidate.Text, "os.Open") {
			foundOpenCandidate = true
			break
		}
	}
	if !foundOpenCandidate {
		t.Fatal("diff did not expose the os.Open candidate needed for AST cleanup validation")
	}
	for _, match := range runRules(parsed, input.repoRoot) {
		if match.RuleID == ruleUnclosedFile {
			t.Fatalf("AST lookup used the nested input path instead of the worktree root: %+v", match)
		}
	}

	snapshot, err := prepareSandboxRepoSnapshot(
		context.Background(),
		input.repoRoot,
		nil,
		defaultSandboxSnapshotLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(snapshot.Root)
	modules, err := prepareAffectedModuleManifest(context.Background(), snapshot.Root, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(modules, []string{"."}) {
		t.Fatalf("affected modules = %#v, want root module", modules)
	}
}

func TestRepositorySnapshotPreservesWhitespaceInCanonicalRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Win32 path normalization does not preserve trailing spaces")
	}
	repoRoot := filepath.Join(t.TempDir(), "repo ")
	if err := os.Mkdir(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	repoRootP1RunGit(t, repoRoot, "init")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n")
	repoRootP1RunGit(t, repoRoot, "add", "review.go")

	snapshot, err := prepareSandboxRepoSnapshot(
		context.Background(),
		repoRoot,
		nil,
		defaultSandboxSnapshotLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(snapshot.Root)
	if _, err := os.Stat(filepath.Join(snapshot.Root, "review.go")); err != nil {
		t.Fatalf("snapshot did not use the canonical whitespace path: %v", err)
	}
}

func TestRepoInputBinaryGoDiffStillRequiresRepositoryChecks(t *testing.T) {
	repoRoot := t.TempDir()
	repoRootP1RunGit(t, repoRoot, "init")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/binary-go\n\ngo 1.21\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, ".gitattributes"), "*.go -diff\n")
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n\nconst Value = 1\n")
	repoRootP1RunGit(t, repoRoot, "add", "go.mod", ".gitattributes", "review.go")
	repoRootP1Commit(t, repoRoot)
	repoRootP1WriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n\nconst Value = 2\n")

	input, err := loadReviewInput(
		context.Background(),
		config{repoPath: repoRoot},
		runGitCommand,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseUnifiedDiff(input.diff)
	if len(parsed.Files) != 1 || !parsed.Files[0].IsBinary || !parsed.Files[0].isGoFile() {
		t.Fatalf("binary Go diff = %+v", parsed)
	}
	if !parseWarningsContain(parsed.Warnings, "Go source path is represented as binary") {
		t.Fatalf("warnings = %+v, want binary Go warning", parsed.Warnings)
	}
	if !hasReviewableGoChange(parsed) {
		t.Fatal("binary Go diff skipped repository checks")
	}
	runner := &recordingSandboxRunner{}
	governance, err := runGovernance(
		context.Background(),
		config{},
		input,
		parsed,
		runtimeHooks{sandboxRunner: runner},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(runner.calls) != 3 || runner.calls[1].Kind != commandCheckGoTest ||
		runner.calls[2].Kind != commandCheckGoVet {
		t.Fatalf("sandbox calls = %+v, want version/test/vet", runner.calls)
	}
	if len(governance.Matches) != 0 {
		t.Fatalf("governance warnings = %+v, want module checks to be available", governance.Matches)
	}
	if conclusion := determineConclusion(reviewReport{
		Parse: reportParse{Warnings: len(parsed.Warnings)},
	}); conclusion != reviewConclusionNeedsHumanReview {
		t.Fatalf("binary Go conclusion = %q, want human review", conclusion)
	}
}

func repoRootP1MinimalDiff(file string) string {
	return strings.Join([]string{
		"diff --git a/" + file + " b/" + file,
		"--- a/" + file,
		"+++ b/" + file,
		"@@ -1 +1 @@",
		"-const Value = 1",
		"+const Value = 2",
		"",
	}, "\n")
}

func repoRootP1WriteFile(t *testing.T, filePath string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func repoRootP1RunGit(t *testing.T, repoRoot string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func repoRootP1Commit(t *testing.T, repoRoot string) {
	t.Helper()
	repoRootP1RunGit(
		t,
		repoRoot,
		"-c", "user.name=Code Review Test",
		"-c", "user.email=code-review@example.invalid",
		"commit", "-m", "baseline",
	)
}
