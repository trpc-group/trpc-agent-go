//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func initGitRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	require.NoError(t, cmd.Run())
	return repo
}

func gitAdd(t *testing.T, repo string, paths ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"add", "--"}, paths...)...)
	cmd.Dir = repo
	require.NoError(t, cmd.Run())
}

func TestStageReviewSnapshotIncludesOnlyTrackedAndReviewChanges(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.secret\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package untracked\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "ignored.secret"), []byte("sentinel-secret\n"), 0o600))
	gitAdd(t, repo, ".gitignore", "tracked.go")

	snapshot, cleanup, err := stageReviewSnapshot(context.Background(), repo, 1024*1024)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	_, err = os.Stat(filepath.Join(snapshot, "tracked.go"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(snapshot, "untracked.go"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(snapshot, "ignored.secret"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(snapshot, ".git"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStageReviewSnapshotRejectsOversizedInput(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "large.go"), []byte("package large\n"), 0o600))
	gitAdd(t, repo, "large.go")
	_, _, err := stageReviewSnapshot(context.Background(), repo, 4)
	require.ErrorContains(t, err, "exceeds")
}

func TestStageReviewSnapshotRejectsTrackedSymlink(t *testing.T) {
	repo := initGitRepository(t)
	external := filepath.Join(t.TempDir(), "sentinel.go")
	require.NoError(t, os.WriteFile(external, []byte("package sentinel\n"), 0o600))
	link := filepath.Join(repo, "linked.go")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	gitAdd(t, repo, "linked.go")
	_, _, err := stageReviewSnapshot(context.Background(), repo, 1024*1024)
	require.ErrorContains(t, err, "not a regular file")
}

func TestHardenedHostConfigEnforcesResources(t *testing.T) {
	config := DefaultSandboxConfig()
	host := hardenedHostConfig(config)
	require.Equal(t, "none", string(host.NetworkMode))
	require.True(t, host.ReadonlyRootfs)
	require.Equal(t, int64(config.MemoryMB)*1024*1024, host.Resources.Memory)
	require.Equal(t, int64(config.CPUPercent)*10_000_000, host.Resources.NanoCPUs)
	require.NotNil(t, host.Resources.PidsLimit)
	require.Equal(t, int64(config.MaxPIDs), *host.Resources.PidsLimit)
	require.Contains(t, host.Tmpfs["/tmp"], "size=268435456")
}

func TestModuleCacheRequiresExplicitTrustedMode(t *testing.T) {
	require.Equal(t, "/tmp/gomodcache", moduleCachePath(false))
	require.Equal(t, "/go/pkg/mod", moduleCachePath(true))
}
