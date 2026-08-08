//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const validPatch = "diff --git a/file.go b/file.go\n--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new\n"

func TestSelectionRequiresExactlyOneSource(t *testing.T) {
	tests := []struct {
		name      string
		selection Selection
		wantError string
	}{
		{name: "diff file", selection: Selection{DiffFile: "change.patch"}},
		{name: "repository", selection: Selection{RepoPath: "."}},
		{name: "fixture", selection: Selection{Fixture: "clean.patch"}},
		{name: "missing", wantError: "exactly one"},
		{
			name: "multiple",
			selection: Selection{
				DiffFile: "change.patch",
				RepoPath: ".",
			},
			wantError: "exactly one",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.selection.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestLoadDiffFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "change.patch")
	require.NoError(t, os.WriteFile(path, []byte(validPatch), 0o600))

	loaded, err := Load(context.Background(), Selection{DiffFile: path})
	require.NoError(t, err)
	require.Equal(t, review.InputSourceDiffFile, loaded.Source)
	require.Equal(t, path, loaded.Reference)
	require.Equal(t, validPatch, string(loaded.Raw))
	require.Len(t, loaded.Diff.Files, 1)
	require.Equal(t, digestRawParts([]rawPart{{layer: DiffLayerUnified, raw: []byte(validPatch)}}), loaded.Digest)
}

func TestLoadFixture(t *testing.T) {
	fixtures := fstest.MapFS{
		"fixtures/clean.patch": {Data: []byte(validPatch)},
	}
	loaded, err := Load(
		context.Background(),
		Selection{Fixture: "clean.patch"},
		WithFixtureFS(fixtures, "fixtures"),
	)
	require.NoError(t, err)
	require.Equal(t, review.InputSourceFixture, loaded.Source)
	require.Equal(t, "clean.patch", loaded.Reference)
	require.Len(t, loaded.Diff.Files, 1)
}

func TestLoadFixtureRejectsTraversal(t *testing.T) {
	_, err := Load(
		context.Background(),
		Selection{Fixture: "../secret.patch"},
		WithFixtureFS(fstest.MapFS{}, "fixtures"),
	)
	require.ErrorContains(t, err, "fixture name")
}

func TestLoadRepositoryUsesFixedGitArgv(t *testing.T) {
	repository := t.TempDir()
	resolved, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	runner := &recordingRunner{root: resolved, diff: validPatch}
	loaded, err := Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(runner),
	)
	require.NoError(t, err)
	require.Equal(t, review.InputSourceRepository, loaded.Source)
	require.Equal(t, resolved, loaded.RepositoryRoot)
	require.Len(t, runner.calls, 5)
	require.Equal(t, []string{"-C", resolved, "rev-parse", "--show-toplevel"}, runner.calls[0].args)
	require.Equal(t, []string{"-C", resolved, "rev-parse", "--verify", "HEAD"}, runner.calls[1].args)
	require.Equal(t, expectedDiffArgs(resolved, testHeadObjectID, true), runner.calls[2].args)
	require.Equal(t, expectedDiffArgs(resolved, "", false), runner.calls[3].args)
	require.Equal(t, []string{"-C", resolved, "cat-file", "blob", ":file.go"}, runner.calls[4].args)
}

func TestDigestBindsRepositoryLayer(t *testing.T) {
	patch := []byte(validPatch)
	staged := digestRawParts([]rawPart{
		{layer: DiffLayerStaged, raw: patch},
		{layer: DiffLayerWorktree, raw: nil},
	})
	worktree := digestRawParts([]rawPart{
		{layer: DiffLayerStaged, raw: nil},
		{layer: DiffLayerWorktree, raw: patch},
	})
	require.NotEqual(t, staged, worktree)
}

func TestLoadRepositoryPreservesTrailingSpaceInRoot(t *testing.T) {
	parent := t.TempDir()
	repository := filepath.Join(parent, "repo ")
	require.NoError(t, os.Mkdir(repository, 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(parent, "repo"), 0o700))
	resolved, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	runner := &recordingRunner{root: resolved, diff: validPatch}
	loaded, err := Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(runner),
	)
	require.NoError(t, err)
	require.Equal(t, resolved, loaded.RepositoryRoot)
	require.Equal(t, resolved, runner.calls[2].args[19])
}

func TestLoadRepositoryResolvesSymlink(t *testing.T) {
	repository := t.TempDir()
	resolved, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	parent := t.TempDir()
	link := filepath.Join(parent, "repo")
	require.NoError(t, os.Symlink(repository, link))
	runner := &recordingRunner{root: resolved, diff: validPatch}

	loaded, err := Load(
		context.Background(),
		Selection{RepoPath: link},
		WithCommandRunner(runner),
	)
	require.NoError(t, err)
	require.Equal(t, resolved, loaded.RepositoryRoot)
	require.Equal(t, resolved, runner.calls[2].args[19])
}

func TestLoadRepositoryResolvesGitRootFromSubdirectory(t *testing.T) {
	repository := t.TempDir()
	root, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	subdirectory := filepath.Join(root, "nested")
	require.NoError(t, os.Mkdir(subdirectory, 0o700))
	runner := &recordingRunner{root: root, diff: validPatch}

	loaded, err := Load(
		context.Background(),
		Selection{RepoPath: subdirectory},
		WithCommandRunner(runner),
	)
	require.NoError(t, err)
	require.Equal(t, root, loaded.RepositoryRoot)
	require.Equal(t, subdirectory, runner.calls[0].args[1])
	require.Equal(t, root, runner.calls[2].args[19])
}

func TestLoadRepositoryUsesEmptyTreeForUnbornRepository(t *testing.T) {
	repository := t.TempDir()
	root, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	runner := &recordingRunner{root: root, diff: validPatch, unborn: true}
	_, err = Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(runner),
	)
	require.NoError(t, err)
	require.Len(t, runner.calls, 8)
	require.Equal(t, []string{"-C", root, "symbolic-ref", "-q", "HEAD"}, runner.calls[2].args)
	require.Equal(t, []string{
		"-C", root, "show-ref", "--verify", "--quiet", "refs/heads/main",
	}, runner.calls[3].args)
	require.Equal(t, []string{
		"-C", root, "hash-object", "-t", "tree", "--stdin",
	}, runner.calls[4].args)
	require.Contains(t, runner.calls[5].args, testEmptyTreeObjectID)
	require.NotContains(t, runner.calls[6].args, testEmptyTreeObjectID)
}

func TestLoadRepositoryIncludesStagedAndUnstagedChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "review@example.com")
	runGit(t, repository, "config", "user.name", "Review Test")
	require.NoError(t, os.WriteFile(
		filepath.Join(repository, "staged.go"),
		[]byte("package fixture\n\nconst staged = 1\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(repository, "unstaged.go"),
		[]byte("package fixture\n\nconst unstaged = 1\n"),
		0o600,
	))
	runGit(t, repository, "add", "staged.go", "unstaged.go")
	runGit(t, repository, "commit", "-m", "initial")

	require.NoError(t, os.WriteFile(
		filepath.Join(repository, "staged.go"),
		[]byte("package fixture\n\nconst staged = 2\n"),
		0o600,
	))
	runGit(t, repository, "add", "staged.go")
	require.NoError(t, os.WriteFile(
		filepath.Join(repository, "unstaged.go"),
		[]byte("package fixture\n\nconst unstaged = 2\n"),
		0o600,
	))

	loaded, err := Load(context.Background(), Selection{RepoPath: repository})
	require.NoError(t, err)
	require.Len(t, loaded.Diff.Files, 2)
	require.Equal(t, DiffLayerStaged, loaded.Diff.Files[0].Layer)
	require.Equal(t, "staged.go", loaded.Diff.Files[0].NewPath)
	require.Equal(t, DiffLayerWorktree, loaded.Diff.Files[1].Layer)
	require.Equal(t, "unstaged.go", loaded.Diff.Files[1].NewPath)
	require.Len(t, loaded.Snapshots, 2)
	require.Equal(t, review.ChangeLayerStaged, loaded.Snapshots[0].Layer)
	require.Contains(t, string(loaded.Snapshots[0].Content), "staged = 2")
	require.Equal(t, review.ChangeLayerWorktree, loaded.Snapshots[1].Layer)
	require.Contains(t, string(loaded.Snapshots[1].Content), "unstaged = 2")
}

func TestLoadRepositoryOverridesDiffFormattingConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "review@example.com")
	runGit(t, repository, "config", "user.name", "Review Test")
	file := filepath.Join(repository, "configured.go")
	require.NoError(t, os.WriteFile(file, []byte("package fixture\n\nconst value = 1\n"), 0o600))
	runGit(t, repository, "add", "configured.go")
	runGit(t, repository, "commit", "-m", "initial")
	require.NoError(t, os.WriteFile(file, []byte("package fixture\n\nconst value = 2\n"), 0o600))
	baseline, err := Load(context.Background(), Selection{RepoPath: repository})
	require.NoError(t, err)

	runGit(t, repository, "config", "diff.suppressBlankEmpty", "true")
	runGit(t, repository, "config", "diff.context", "0")
	runGit(t, repository, "config", "diff.algorithm", "patience")
	runGit(t, repository, "config", "diff.indentHeuristic", "true")
	runGit(t, repository, "config", "diff.interHunkContext", "99")
	runGit(t, repository, "config", "diff.submodule", "log")
	configured, err := Load(context.Background(), Selection{RepoPath: repository})
	require.NoError(t, err)
	require.Equal(t, baseline.Raw, configured.Raw)
	require.Equal(t, baseline.Digest, configured.Digest)
}

func TestLoadRepositoryPreservesStagedChangeRevertedInWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "review@example.com")
	runGit(t, repository, "config", "user.name", "Review Test")
	path := filepath.Join(repository, "layered.go")
	original := []byte("package fixture\n\nconst value = 1\n")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	runGit(t, repository, "add", "layered.go")
	runGit(t, repository, "commit", "-m", "initial")
	require.NoError(t, os.WriteFile(
		path,
		[]byte("package fixture\n\nconst value = 2\n"),
		0o600,
	))
	runGit(t, repository, "add", "layered.go")
	require.NoError(t, os.WriteFile(path, original, 0o600))

	loaded, err := Load(context.Background(), Selection{RepoPath: repository})
	require.NoError(t, err)
	require.Len(t, loaded.Diff.Files, 2)
	require.Equal(t, DiffLayerStaged, loaded.Diff.Files[0].Layer)
	require.Equal(t, DiffLayerWorktree, loaded.Diff.Files[1].Layer)
}

func TestLoadRepositorySupportsUnbornRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	runGit(t, repository, "init")
	require.NoError(t, os.WriteFile(
		filepath.Join(repository, "new.go"),
		[]byte("package fixture\n\nconst value = 1\n"),
		0o600,
	))
	runGit(t, repository, "add", "new.go")

	loaded, err := Load(context.Background(), Selection{RepoPath: repository})
	require.NoError(t, err)
	require.Len(t, loaded.Diff.Files, 1)
	require.Equal(t, ChangeAdded, loaded.Diff.Files[0].Change)
	require.Equal(t, "new.go", loaded.Diff.Files[0].NewPath)
}

func TestLoadRejectsOversizedSources(t *testing.T) {
	patch := validPatch + strings.Repeat("x", 64)
	tests := []struct {
		name      string
		selection Selection
		options   []SourceOption
	}{
		{
			name:      "fixture",
			selection: Selection{Fixture: "large.patch"},
			options: []SourceOption{
				WithFixtureFS(fstest.MapFS{"large.patch": {Data: []byte(patch)}}, "."),
			},
		},
		{
			name:      "repository",
			selection: Selection{RepoPath: t.TempDir()},
			options: []SourceOption{
				WithCommandRunner(&recordingRunner{diff: patch}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.options = append(tt.options, WithSourceMaxBytes(len(validPatch)))
			_, err := Load(context.Background(), tt.selection, tt.options...)
			require.ErrorContains(t, err, "input byte limit")
		})
	}
}

func TestLoadAllowsExactLimitStagedOnlyDiffWithoutTrailingNewline(t *testing.T) {
	patch := strings.TrimSuffix(validPatch, "\n")
	repository := t.TempDir()
	loaded, err := Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(&recordingRunner{diff: patch}),
		WithSourceMaxBytes(len(patch)),
	)
	require.NoError(t, err)
	require.Equal(t, patch, string(loaded.Raw))
}

func TestLoadPropagatesContextCancellationForEverySource(t *testing.T) {
	directory := t.TempDir()
	diffFile := filepath.Join(directory, "change.patch")
	require.NoError(t, os.WriteFile(diffFile, []byte(validPatch), 0o600))
	tests := []struct {
		name      string
		selection Selection
		options   []SourceOption
	}{
		{name: "diff file", selection: Selection{DiffFile: diffFile}},
		{
			name:      "fixture",
			selection: Selection{Fixture: "clean.patch"},
			options: []SourceOption{
				WithFixtureFS(fstest.MapFS{"clean.patch": {Data: []byte(validPatch)}}, "."),
			},
		},
		{name: "repository", selection: Selection{RepoPath: directory}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := Load(ctx, tt.selection, tt.options...)
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestLoadHonorsStricterParserInputLimit(t *testing.T) {
	fixtures := fstest.MapFS{"large.patch": {Data: []byte(validPatch)}}
	_, err := Load(
		context.Background(),
		Selection{Fixture: "large.patch"},
		WithFixtureFS(fixtures, "."),
		WithSourceMaxBytes(len(validPatch)+100),
		WithParseOptions(WithLimits(Limits{MaxInputBytes: len(validPatch) - 1})),
	)
	require.ErrorContains(t, err, "input byte limit")
}

func TestLoadRejectsNilOptionsAndDependencies(t *testing.T) {
	_, err := Load(context.Background(), Selection{DiffFile: "x"}, nil)
	require.ErrorContains(t, err, "nil option")

	_, err = Load(
		context.Background(),
		Selection{RepoPath: t.TempDir()},
		WithCommandRunner(nil),
	)
	require.ErrorContains(t, err, "nil command runner")

	_, err = Load(
		context.Background(),
		Selection{Fixture: "x.patch"},
		WithFixtureFS(nil, "."),
	)
	require.ErrorContains(t, err, "nil fixture filesystem")
}

func TestLoadWrapsCommandFailure(t *testing.T) {
	sentinel := errors.New("git failed")
	_, err := Load(
		context.Background(),
		Selection{RepoPath: t.TempDir()},
		WithCommandRunner(commandRunnerFunc(func(
			context.Context,
			string,
			[]string,
			io.Reader,
			io.Writer,
		) error {
			return sentinel
		})),
	)
	require.ErrorIs(t, err, sentinel)
}

func TestLoadDoesNotTreatUnexpectedHEADFailureAsUnborn(t *testing.T) {
	repository := t.TempDir()
	root, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	sentinel := errors.New("temporary runner failure")
	calls := 0
	_, err = Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(commandRunnerFunc(func(
			_ context.Context,
			_ string,
			args []string,
			_ io.Reader,
			stdout io.Writer,
		) error {
			calls++
			if containsArgs(args, "rev-parse", "--show-toplevel") {
				_, writeErr := io.WriteString(stdout, root+"\n")
				return writeErr
			}
			return sentinel
		})),
	)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 2, calls)
}

func TestLoadDoesNotTreatExistingBrokenHEADAsUnborn(t *testing.T) {
	repository := t.TempDir()
	root, err := filepath.EvalSymlinks(repository)
	require.NoError(t, err)
	original := errors.New("cannot resolve HEAD")
	calls := 0
	_, err = Load(
		context.Background(),
		Selection{RepoPath: repository},
		WithCommandRunner(commandRunnerFunc(func(
			_ context.Context,
			_ string,
			args []string,
			_ io.Reader,
			stdout io.Writer,
		) error {
			calls++
			switch {
			case containsArgs(args, "rev-parse", "--show-toplevel"):
				_, writeErr := io.WriteString(stdout, root+"\n")
				return writeErr
			case containsArgs(args, "rev-parse", "--verify", "HEAD"):
				return &commandExitError{code: 128, err: original}
			case containsArgs(args, "symbolic-ref", "-q", "HEAD"):
				_, writeErr := io.WriteString(stdout, "refs/heads/main\n")
				return writeErr
			case containsArgs(args, "show-ref", "--verify", "--quiet"):
				return nil
			default:
				return errors.New("unexpected command")
			}
		})),
	)
	require.ErrorIs(t, err, original)
	require.Equal(t, 4, calls)
}

func TestBoundedWriterCannotBypassLimitThroughReadFrom(t *testing.T) {
	writer := newBoundedWriter(4)
	reader := struct{ io.Reader }{Reader: strings.NewReader("oversized")}
	_, err := io.Copy(writer, reader)
	require.ErrorIs(t, err, errSourceLimit)
	require.True(t, writer.exceeded)
	require.LessOrEqual(t, len(writer.Bytes()), 4)
}

type recordingRunner struct {
	root   string
	diff   string
	unborn bool
	calls  []commandCall
}

const (
	testHeadObjectID      = "0123456789012345678901234567890123456789"
	testEmptyTreeObjectID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
)

type commandCall struct {
	name string
	args []string
}

func (r *recordingRunner) Run(
	_ context.Context,
	name string,
	args []string,
	_ io.Reader,
	stdout io.Writer,
) error {
	r.calls = append(r.calls, commandCall{name: name, args: append([]string(nil), args...)})
	var output string
	switch {
	case containsArgs(args, "rev-parse", "--show-toplevel"):
		if r.root == "" {
			root, err := filepath.EvalSymlinks(args[1])
			if err != nil {
				return err
			}
			r.root = root
		}
		output = r.root + "\n"
	case containsArgs(args, "rev-parse", "--verify", "HEAD"):
		if r.unborn {
			return &commandExitError{code: 128, err: errors.New("unknown revision HEAD")}
		}
		output = testHeadObjectID + "\n"
	case containsArgs(args, "symbolic-ref", "-q", "HEAD"):
		output = "refs/heads/main\n"
	case containsArgs(args, "show-ref", "--verify", "--quiet"):
		return &commandExitError{code: 1, err: errors.New("reference does not exist")}
	case containsArgs(args, "hash-object", "-t", "tree", "--stdin"):
		output = testEmptyTreeObjectID + "\n"
	case containsArgs(args, "cat-file", "blob"):
		output = "package fixture\n"
	case containsArg(args, "diff"):
		if containsArg(args, "--cached") {
			output = r.diff
		}
	default:
		return errors.New("unexpected git command")
	}
	_, err := io.WriteString(stdout, output)
	return err
}

func containsArgs(args []string, sequence ...string) bool {
	for index := 0; index+len(sequence) <= len(args); index++ {
		if strings.Join(args[index:index+len(sequence)], "\x00") == strings.Join(sequence, "\x00") {
			return true
		}
	}
	return false
}

func containsArg(args []string, value string) bool {
	for _, argument := range args {
		if argument == value {
			return true
		}
	}
	return false
}

func expectedDiffArgs(root, base string, cached bool) []string {
	args := []string{
		"-c", "color.ui=false",
		"-c", "core.quotePath=true",
		"-c", "diff.suppressBlankEmpty=false",
		"-c", "diff.submodule=short",
		"-c", "diff.context=3",
		"-c", "diff.interHunkContext=0",
		"-c", "diff.algorithm=myers",
		"-c", "diff.indentHeuristic=false",
		"-c", "diff.renameLimit=10000",
		"-C", root,
		"diff",
		"--binary",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--unified=3",
		"--inter-hunk-context=0",
		"--submodule=short",
		"--src-prefix=a/",
		"--dst-prefix=b/",
		"--find-renames=50%",
		"--find-copies=50%",
		"-O/dev/null",
	}
	if cached {
		args = append(args, "--cached")
	}
	if base != "" {
		args = append(args, base)
	}
	return append(args, "--")
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}

type commandRunnerFunc func(context.Context, string, []string, io.Reader, io.Writer) error

func (f commandRunnerFunc) Run(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	return f(ctx, name, args, stdin, stdout)
}
