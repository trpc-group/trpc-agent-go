//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

const (
	testRunID        = "subagent:test-run"
	testFileName     = "README.md"
	testFileContent  = "hello\n"
	testCommitMsg    = "initial"
	testUserEmail    = "test@example.com"
	testUserName     = "Test User"
	testBranchPrefix = "test-worktree-"
	testPRBranch     = "fix-issue-1"
	testHeadCommit   = "abc123"
	testCollidingID1 = "!!!"
	testCollidingID2 = "???"

	testUnexpectedGitCommand = "unexpected git command"
	sameIDCreateAttempts     = 2
)

var errTestGitFailure = errors.New("test git failure")

type createResult struct {
	lease Lease
	err   error
}

func TestManagerCreateAndFinalizeCleanWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(
		filepath.Join(t.TempDir(), "worktrees"),
		WithBranchPrefix(testBranchPrefix),
	)

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.DirExists(t, lease.Path)
	require.Equal(t, repo, lease.RepoRoot)
	require.NotEmpty(t, lease.BaseCommit)
	require.Contains(t, lease.Branch, testBranchPrefix)

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Removed)
	require.False(t, result.Preserved)
	require.NoDirExists(t, lease.Path)

	branches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Empty(t, branches)
}

func TestManagerFinalizePreservesChangedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(lease.Path, testFileName),
			[]byte("changed\n"),
			0o644,
		),
	)

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonStatus, result.Reason)
	require.DirExists(t, lease.Path)
}

func TestManagerFinalizePreservesCommittedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(lease.Path, testFileName),
			[]byte("changed\n"),
			0o644,
		),
	)
	gitRun(t, lease.Path, "add", testFileName)
	gitRun(t, lease.Path, "commit", "-m", "worktree change")

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonCommit, result.Reason)
	require.DirExists(t, lease.Path)
}

func TestManagerFinalizePreservesCommittedFeatureBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	gitRun(t, lease.Path, "checkout", "-b", testPRBranch)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(lease.Path, testFileName),
			[]byte("changed\n"),
			0o644,
		),
	)
	gitRun(t, lease.Path, "add", testFileName)
	gitRun(t, lease.Path, "commit", "-m", "feature branch change")

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonCommit, result.Reason)
	require.Equal(t, testPRBranch, result.Branch)
	require.DirExists(t, lease.Path)

	managedBranches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Empty(t, managedBranches)
	prBranches := gitOutput(t, repo, "branch", "--list", testPRBranch)
	require.Contains(t, prBranches, testPRBranch)
}

func TestManagerFinalizePreservesManagedBranchCommitsAfterCheckout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(lease.Path, testFileName),
			[]byte("managed branch change\n"),
			0o644,
		),
	)
	gitRun(t, lease.Path, "add", testFileName)
	gitRun(t, lease.Path, "commit", "-m", "managed branch change")
	gitRun(t, lease.Path, "checkout", "-b", testPRBranch, lease.BaseCommit)

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonCommit, result.Reason)
	require.Equal(t, testPRBranch, result.Branch)
	require.DirExists(t, lease.Path)

	managedBranches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Contains(t, managedBranches, lease.Branch)
}

func TestManagerCreateRejectsDirtySource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(repo, testFileName),
			[]byte("dirty\n"),
			0o644,
		),
	)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	_, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorIs(t, err, ErrDirtySource)
}

func TestManagerCreateRejectsExistingManagedBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(
		filepath.Join(t.TempDir(), "worktrees"),
		WithBranchPrefix(testBranchPrefix),
	)
	branch := testBranchPrefix + leaseSlug(testRunID)
	gitRun(t, repo, "branch", branch)

	_, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorContains(t, err, "branch already exists")

	branches := gitOutput(t, repo, "branch", "--list", branch)
	require.Contains(t, branches, branch)
}

func TestManagerCreateSerializesSameID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))
	results := make(chan createResult, sameIDCreateAttempts)
	start := make(chan struct{})
	for i := 0; i < sameIDCreateAttempts; i++ {
		go func() {
			<-start
			lease, err := manager.Create(ctx, CreateRequest{
				ID:      testRunID,
				Workdir: repo,
			})
			results <- createResult{lease: lease, err: err}
		}()
	}

	close(start)

	var lease Lease
	var createErr error
	for i := 0; i < sameIDCreateAttempts; i++ {
		result := <-results
		if result.err == nil {
			lease = result.lease
			continue
		}
		createErr = result.err
	}

	require.NotEmpty(t, lease.Path)
	require.Error(t, createErr)
	require.ErrorContains(t, createErr, "already exists")
	require.DirExists(t, lease.Path)
	branches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Contains(t, branches, lease.Branch)
}

func TestManagerCreateAllowsCollidingSafeSlugs(t *testing.T) {
	t.Parallel()

	require.Equal(t, safeSlug(testCollidingID1), safeSlug(testCollidingID2))
	require.NotEqual(t, leaseSlug(testCollidingID1), leaseSlug(testCollidingID2))

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	first, err := manager.Create(ctx, CreateRequest{
		ID:      testCollidingID1,
		Workdir: repo,
	})
	require.NoError(t, err)
	second, err := manager.Create(ctx, CreateRequest{
		ID:      testCollidingID2,
		Workdir: repo,
	})
	require.NoError(t, err)

	require.NotEqual(t, first.Path, second.Path)
	require.NotEqual(t, first.Branch, second.Branch)
	require.DirExists(t, first.Path)
	require.DirExists(t, second.Path)
}

func TestLeaseSlugTruncatesWithoutSplittingRunes(t *testing.T) {
	t.Parallel()

	slug := leaseSlug(strings.Repeat("界", maxSlugLength))

	require.True(t, utf8.ValidString(slug))
	require.LessOrEqual(t, len(slug), maxSlugLength)
	require.Contains(t, slug, slugHashSeparator)
}

func TestManagerCreateCleansPartialWorktreeOnAddFailure(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("simulated add failure")
	ctx := context.Background()
	repo := newGitRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	manager := NewManager(root)
	manager.runGit = func(
		ctx context.Context,
		dir string,
		args ...string,
	) (string, error) {
		if len(args) == 6 && args[0] == "worktree" && args[1] == "add" {
			branch := args[3]
			path := args[4]
			baseCommit := args[5]
			require.NoError(t, os.MkdirAll(path, defaultDirPerm))
			_, err := runGitCommand(ctx, dir, "branch", branch, baseCommit)
			require.NoError(t, err)
			return "", expectedErr
		}
		return runGitCommand(ctx, dir, args...)
	}

	_, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorIs(t, err, expectedErr)

	branch := defaultBranchPrefix + leaseSlug(testRunID)
	path := filepath.Join(root, repoHash(repo), leaseSlug(testRunID))
	require.NoDirExists(t, path)
	branches := gitOutput(t, repo, "branch", "--list", branch)
	require.Empty(t, branches)
}

func TestManagerFinalizeTreatsMissingWorktreeAsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(lease.Path))

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.True(t, result.Removed)
	require.False(t, result.Preserved)
	require.Equal(t, changeReasonRemoved, result.Reason)

	branches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Empty(t, branches)
	worktrees := gitOutput(t, repo, "worktree", "list", "--porcelain")
	require.NotContains(t, worktrees, lease.Path)
}

func TestManagerFinalizePreservesMissingCommittedBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(lease.Path, testFileName),
			[]byte("changed\n"),
			0o644,
		),
	)
	gitRun(t, lease.Path, "add", testFileName)
	gitRun(t, lease.Path, "commit", "-m", "worktree change")
	require.NoError(t, os.RemoveAll(lease.Path))

	result, err := manager.Finalize(ctx, lease)
	require.NoError(t, err)
	require.False(t, result.Removed)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonCommit, result.Reason)

	branches := gitOutput(t, repo, "branch", "--list", lease.Branch)
	require.Contains(t, branches, lease.Branch)
}

func TestManagerFinalizePreservesOnStatusError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("status failed")
	lease := newSyntheticLease(t)
	manager := newManagerForLease(lease)
	manager.runGit = func(
		context.Context,
		string,
		...string,
	) (string, error) {
		return "", expectedErr
	}
	result, err := manager.Finalize(context.Background(), lease)
	require.ErrorIs(t, err, expectedErr)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonStatusError, result.Reason)
}

func TestManagerOptionsAndHelpers(t *testing.T) {
	t.Parallel()

	timeout := time.Second
	manager := NewManager(
		"relative-worktrees",
		WithCommandTimeout(timeout),
		WithCommandTimeout(0),
		WithBranchPrefix("  custom-worktree-  "),
		WithBranchPrefix(" "),
	)
	require.Equal(t, timeout, manager.CommandTimeout)
	require.Equal(t, "custom-worktree-", manager.branchPrefix())

	var nilManager *Manager
	require.Equal(t, defaultBranchPrefix, nilManager.branchPrefix())

	manager.BranchPrefix = ""
	require.Equal(t, defaultBranchPrefix, manager.branchPrefix())

	manager.clock = nil
	require.WithinDuration(t, time.Now(), manager.now(), time.Second)

	root, err := normalizeRoot("relative-worktrees")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(root))
	_, err = normalizeRoot("")
	require.ErrorContains(t, err, "empty root")

	require.Equal(t, "run", safeSlug(" ! "))
	require.Equal(t, "a-b", safeSlug("A??B?"))
	require.Len(t, safeSlug(strings.Repeat("a", maxSlugLength+1)), maxSlugLength)

	require.ErrorContains(t, validateLease(Lease{}, defaultBranchPrefix), "empty lease path")
	require.ErrorContains(t, validateLease(Lease{
		Path: t.TempDir(),
	}, defaultBranchPrefix), "empty lease repo root")
	require.ErrorContains(t, validateLease(Lease{
		Path:     t.TempDir(),
		RepoRoot: t.TempDir(),
	}, defaultBranchPrefix), "empty lease base commit")
	require.ErrorContains(t, validateLease(Lease{
		Path:       t.TempDir(),
		RepoRoot:   t.TempDir(),
		BaseCommit: testHeadCommit,
		Branch:     testPRBranch,
	}, defaultBranchPrefix), "unmanaged branch")
}

func TestManagerCreateValidationAndSetupErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")

	var nilManager *Manager
	_, err := nilManager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorContains(t, err, "nil manager")

	_, err = NewManager("").Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorContains(t, err, "empty root")

	_, err = NewManager(root).Create(ctx, CreateRequest{
		Workdir: repo,
	})
	require.ErrorContains(t, err, "empty id")

	_, err = NewManager(root).Create(ctx, CreateRequest{
		ID: testRunID,
	})
	require.ErrorContains(t, err, "empty workdir")

	path := filepath.Join(root, repoHash(repo), leaseSlug(testRunID))
	require.NoError(t, os.MkdirAll(path, defaultDirPerm))
	_, err = NewManager(root).Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorContains(t, err, "path already exists")
}

func TestManagerCreatePropagatesGitErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newFakeRepoRoot(t)

	tests := []struct {
		name      string
		runGit    commandRunner
		wantError string
	}{
		{
			name: "repo root command error",
			runGit: func(context.Context, string, ...string) (string, error) {
				return "", errTestGitFailure
			},
			wantError: "resolve repo root",
		},
		{
			name: "invalid repo root",
			runGit: func(context.Context, string, ...string) (string, error) {
				return t.TempDir(), nil
			},
			wantError: "invalid repo root",
		},
		{
			name: "source status error",
			runGit: fakeCreateRunner(repo, map[string]fakeGitResult{
				"status --porcelain": {err: errTestGitFailure},
			}),
			wantError: "inspect source status",
		},
		{
			name: "head error",
			runGit: fakeCreateRunner(repo, map[string]fakeGitResult{
				"rev-parse HEAD": {err: errTestGitFailure},
			}),
			wantError: "resolve HEAD",
		},
		{
			name: "empty head",
			runGit: fakeCreateRunner(repo, map[string]fakeGitResult{
				"rev-parse HEAD": {out: "\n"},
			}),
			wantError: "empty HEAD",
		},
		{
			name: "branch list error",
			runGit: fakeCreateRunner(repo, map[string]fakeGitResult{
				"branch --list " + defaultBranchPrefix + leaseSlug(testRunID): {
					err: errTestGitFailure,
				},
			}),
			wantError: "inspect branch",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))
			manager.runGit = tt.runGit

			_, err := manager.Create(ctx, CreateRequest{
				ID:      testRunID,
				Workdir: repo,
			})
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestManagerCreateAllowsDirtySourceWhenRequested(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(repo, testFileName),
			[]byte("dirty\n"),
			0o644,
		),
	)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))

	lease, err := manager.Create(ctx, CreateRequest{
		ID:         testRunID,
		Workdir:    repo,
		AllowDirty: true,
	})
	require.NoError(t, err)
	require.DirExists(t, lease.Path)
}

func TestManagerCreateReportsCleanupFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newGitRepo(t)
	manager := NewManager(filepath.Join(t.TempDir(), "worktrees"))
	manager.runGit = func(
		ctx context.Context,
		dir string,
		args ...string,
	) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "add" {
			require.NoError(t, os.MkdirAll(args[4], defaultDirPerm))
			return "", errTestGitFailure
		}
		if strings.Join(args, " ") == "worktree prune" {
			return "", errTestGitFailure
		}
		return runGitCommand(ctx, dir, args...)
	}

	_, err := manager.Create(ctx, CreateRequest{
		ID:      testRunID,
		Workdir: repo,
	})
	require.ErrorContains(t, err, "cleanup")
	require.ErrorContains(t, err, "prune partial worktree")
}

func TestManagerFinalizeValidationErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	lease := Lease{
		Path:       t.TempDir(),
		RepoRoot:   t.TempDir(),
		Branch:     defaultBranchPrefix + "run",
		BaseCommit: testHeadCommit,
	}

	var nilManager *Manager
	result, err := nilManager.Finalize(ctx, lease)
	require.ErrorContains(t, err, "nil manager")
	require.True(t, result.Preserved)
	require.Equal(t, changeReasonRemoveError, result.Reason)

	manager := NewManager(t.TempDir())
	for _, tt := range []struct {
		name      string
		lease     Lease
		wantError string
	}{
		{name: "empty path", lease: Lease{}, wantError: "empty lease path"},
		{
			name: "empty repo root",
			lease: Lease{
				Path: t.TempDir(),
			},
			wantError: "empty lease repo root",
		},
		{
			name: "empty base commit",
			lease: Lease{
				Path:     t.TempDir(),
				RepoRoot: t.TempDir(),
			},
			wantError: "empty lease base commit",
		},
		{
			name: "unmanaged branch",
			lease: Lease{
				Path:       t.TempDir(),
				RepoRoot:   t.TempDir(),
				BaseCommit: testHeadCommit,
				Branch:     testPRBranch,
			},
			wantError: "unmanaged branch",
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := manager.Finalize(ctx, tt.lease)
			require.ErrorContains(t, err, tt.wantError)
			require.True(t, result.Preserved)
			require.Equal(t, changeReasonRemoveError, result.Reason)
		})
	}
}

func TestManagerFinalizeRejectsForeignLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name      string
		mutate    func(Lease) Lease
		wantError string
	}{
		{
			name: "empty id",
			mutate: func(lease Lease) Lease {
				lease.ID = ""
				return lease
			},
			wantError: "empty lease id",
		},
		{
			name: "branch mismatch",
			mutate: func(lease Lease) Lease {
				lease.Branch = defaultBranchPrefix + "other"
				return lease
			},
			wantError: "lease branch mismatch",
		},
		{
			name: "path mismatch",
			mutate: func(lease Lease) Lease {
				lease.Path = filepath.Join(
					filepath.Dir(filepath.Dir(lease.Path)),
					"foreign",
				)
				return lease
			},
			wantError: "lease path mismatch",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lease := newSyntheticLease(t)
			manager := newManagerForLease(lease)
			manager.runGit = func(
				context.Context,
				string,
				...string,
			) (string, error) {
				t.Fatal("git should not run for foreign lease")
				return "", nil
			}

			result, err := manager.Finalize(ctx, tt.mutate(lease))
			require.ErrorContains(t, err, tt.wantError)
			require.True(t, result.Preserved)
			require.Equal(t, changeReasonRemoveError, result.Reason)
		})
	}
}

func TestManagerFinalizePropagatesChangeInspectionErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name       string
		overrides  map[string]fakeGitResult
		wantReason string
		wantError  string
	}{
		{
			name: "status error",
			overrides: map[string]fakeGitResult{
				"status --porcelain": {err: errTestGitFailure},
			},
			wantReason: changeReasonStatusError,
			wantError:  "inspect status",
		},
		{
			name: "commit count error",
			overrides: map[string]fakeGitResult{
				"rev-list --count " + testHeadCommit + "..HEAD": {
					err: errTestGitFailure,
				},
			},
			wantReason: changeReasonCommitError,
			wantError:  "inspect commits",
		},
		{
			name: "commit count parse error",
			overrides: map[string]fakeGitResult{
				"rev-list --count " + testHeadCommit + "..HEAD": {out: "bad"},
			},
			wantReason: changeReasonCommitError,
			wantError:  "parse commit count",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lease := newSyntheticLease(t)
			manager := newManagerForLease(lease)
			manager.runGit = fakeFinalizeRunner(lease, tt.overrides)

			result, err := manager.Finalize(ctx, lease)
			require.ErrorContains(t, err, tt.wantError)
			require.True(t, result.Preserved)
			require.True(t, result.HasChanges)
			require.Equal(t, tt.wantReason, result.Reason)
		})
	}
}

func TestManagerFinalizePropagatesRemovalErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name      string
		overrides map[string]fakeGitResult
	}{
		{
			name: "worktree remove error",
			overrides: map[string]fakeGitResult{
				"worktree remove --force {path}": {err: errTestGitFailure},
			},
		},
		{
			name: "branch delete error",
			overrides: map[string]fakeGitResult{
				"branch -D {branch}": {err: errTestGitFailure},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lease := newSyntheticLease(t)
			overrides := replaceLeasePlaceholders(lease, tt.overrides)
			manager := newManagerForLease(lease)
			manager.runGit = fakeFinalizeRunner(lease, overrides)

			result, err := manager.Finalize(ctx, lease)
			require.Error(t, err)
			require.Equal(t, changeReasonRemoveError, result.Reason)
		})
	}
}

func TestManagerFinalizeDeletesManagedBranchOnFeatureBranchFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	lease := newSyntheticLease(t)
	manager := newManagerForLease(lease)
	manager.runGit = fakeFinalizeRunner(lease, map[string]fakeGitResult{
		"branch --show-current": {out: testPRBranch},
		"status --porcelain":    {out: " M " + testFileName},
		"branch -D " + lease.Branch: {
			err: errTestGitFailure,
		},
	})

	result, err := manager.Finalize(ctx, lease)
	require.ErrorIs(t, err, errTestGitFailure)
	require.True(t, result.Preserved)
	require.True(t, result.HasChanges)
	require.Equal(t, changeReasonRemoveError, result.Reason)
}

func TestManagerFinalizeMissingPathErrorBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name        string
		overrides   map[string]fakeGitResult
		wantRemoved bool
		wantReason  string
		wantError   string
	}{
		{
			name: "prune error",
			overrides: map[string]fakeGitResult{
				"worktree prune": {err: errTestGitFailure},
			},
			wantReason: changeReasonRemoveError,
			wantError:  "prune missing worktree",
		},
		{
			name: "branch inspect error",
			overrides: map[string]fakeGitResult{
				"branch --list {branch}": {err: errTestGitFailure},
			},
			wantReason: changeReasonCommitError,
			wantError:  "inspect branch",
		},
		{
			name: "branch missing",
			overrides: map[string]fakeGitResult{
				"branch --list {branch}": {out: ""},
			},
			wantRemoved: true,
			wantReason:  changeReasonRemoved,
		},
		{
			name: "branch commit count error",
			overrides: map[string]fakeGitResult{
				"rev-list --count {base}..{branch}": {err: errTestGitFailure},
			},
			wantReason: changeReasonCommitError,
			wantError:  "inspect branch commits",
		},
		{
			name: "branch commit count parse error",
			overrides: map[string]fakeGitResult{
				"rev-list --count {base}..{branch}": {out: "bad"},
			},
			wantReason: changeReasonCommitError,
			wantError:  "parse branch commit count",
		},
		{
			name: "delete branch error",
			overrides: map[string]fakeGitResult{
				"branch -D {branch}": {err: errTestGitFailure},
			},
			wantReason: changeReasonRemoveError,
			wantError:  "delete branch",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lease := newSyntheticLease(t)
			require.NoError(t, os.RemoveAll(lease.Path))
			manager := newManagerForLease(lease)
			manager.runGit = fakeFinalizeRunner(
				lease,
				replaceLeasePlaceholders(lease, tt.overrides),
			)

			result, err := manager.Finalize(ctx, lease)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.True(t, result.Preserved)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantRemoved, result.Removed)
			require.Equal(t, tt.wantReason, result.Reason)
		})
	}
}

func TestManagerGitAndRepoHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	manager := NewManager(t.TempDir())
	manager.runGit = nil
	_, err := manager.git(ctx, t.TempDir(), "status")
	require.ErrorContains(t, err, "nil command runner")

	manager.runGit = func(context.Context, string, ...string) (string, error) {
		return "\n", nil
	}
	_, err = manager.repoRoot(ctx, t.TempDir())
	require.ErrorContains(t, err, "empty repo root")

	manager.runGit = func(context.Context, string, ...string) (string, error) {
		return t.TempDir(), nil
	}
	_, err = manager.repoRoot(ctx, t.TempDir())
	require.ErrorContains(t, err, "invalid repo root")

	manager.runGit = func(context.Context, string, ...string) (string, error) {
		return "", errTestGitFailure
	}
	dirty, err := manager.hasDirtySource(ctx, t.TempDir())
	require.ErrorContains(t, err, "inspect source status")
	require.False(t, dirty)
}

type fakeGitResult struct {
	out string
	err error
}

func fakeCreateRunner(
	repo string,
	overrides map[string]fakeGitResult,
) commandRunner {
	return func(
		_ context.Context,
		_ string,
		args ...string,
	) (string, error) {
		key := strings.Join(args, " ")
		if result, ok := overrides[key]; ok {
			return result.out, result.err
		}
		switch key {
		case "rev-parse --show-toplevel":
			return repo, nil
		case "status --porcelain":
			return "", nil
		case "rev-parse HEAD":
			return testHeadCommit, nil
		case "branch --list " + defaultBranchPrefix + leaseSlug(testRunID):
			return "", nil
		default:
			return "", errors.New(testUnexpectedGitCommand + ": " + key)
		}
	}
}

func fakeFinalizeRunner(
	lease Lease,
	overrides map[string]fakeGitResult,
) commandRunner {
	return func(
		_ context.Context,
		_ string,
		args ...string,
	) (string, error) {
		key := strings.Join(args, " ")
		if result, ok := overrides[key]; ok {
			return result.out, result.err
		}
		switch key {
		case "branch --show-current":
			return lease.Branch, nil
		case "status --porcelain":
			return "", nil
		case "rev-list --count " + lease.BaseCommit + "..HEAD":
			return "0", nil
		case "worktree remove --force " + lease.Path:
			return "", nil
		case "worktree prune":
			return "", nil
		case "branch --list " + lease.Branch:
			return lease.Branch, nil
		case "rev-list --count " + lease.BaseCommit + ".." + lease.Branch:
			return "0", nil
		case "branch -D " + lease.Branch:
			return "", nil
		default:
			return "", errors.New(testUnexpectedGitCommand + ": " + key)
		}
	}
}

func replaceLeasePlaceholders(
	lease Lease,
	values map[string]fakeGitResult,
) map[string]fakeGitResult {
	out := make(map[string]fakeGitResult, len(values))
	for key, value := range values {
		key = strings.ReplaceAll(key, "{path}", lease.Path)
		key = strings.ReplaceAll(key, "{branch}", lease.Branch)
		key = strings.ReplaceAll(key, "{base}", lease.BaseCommit)
		out[key] = value
	}
	return out
}

func newSyntheticLease(t *testing.T) Lease {
	t.Helper()

	root := t.TempDir()
	repo := t.TempDir()
	slug := leaseSlug(testRunID)
	path := filepath.Join(root, repoHash(repo), slug)
	require.NoError(t, os.MkdirAll(path, defaultDirPerm))
	return Lease{
		ID:         testRunID,
		Path:       path,
		RepoRoot:   repo,
		Branch:     defaultBranchPrefix + slug,
		BaseCommit: testHeadCommit,
	}
}

func newManagerForLease(lease Lease) *Manager {
	return NewManager(filepath.Dir(filepath.Dir(lease.Path)))
}

func newFakeRepoRoot(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, gitDirName), defaultDirPerm))
	return repo
}

func newGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	gitRun(t, repo, "init")
	gitRun(t, repo, "config", "user.email", testUserEmail)
	gitRun(t, repo, "config", "user.name", testUserName)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(repo, testFileName),
			[]byte(testFileContent),
			0o644,
		),
	)
	gitRun(t, repo, "add", testFileName)
	gitRun(t, repo, "commit", "-m", testCommitMsg)
	canonical, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	return canonical
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()

	_ = gitOutput(t, dir, args...)
}
