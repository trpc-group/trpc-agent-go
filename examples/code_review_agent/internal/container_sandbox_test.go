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
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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

func gitCommit(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command(
		"git",
		"-c", "user.name=test",
		"-c", "user.email=test@example.com",
		"commit", "-m", "test snapshot",
	)
	cmd.Dir = repo
	require.NoError(t, cmd.Run())
}

func TestStageReviewSnapshotIncludesOnlyTrackedAndReviewChanges(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.secret\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600))
	gitAdd(t, repo, ".gitignore", "tracked.go")
	gitCommit(t, repo)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package modified\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package untracked\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "ignored.secret"), []byte("sentinel-secret\n"), 0o600))

	snapshot, cleanup, err := stageReviewSnapshot(context.Background(), repo, 1024*1024)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	data, err := os.ReadFile(filepath.Join(snapshot, "tracked.go"))
	require.NoError(t, err)
	require.Equal(t, "package modified\n", string(data))
	_, err = os.Stat(filepath.Join(snapshot, "tracked.go"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(snapshot, "untracked.go"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(snapshot, "ignored.secret"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(snapshot, ".git"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStageReviewSnapshotAppliesOnlySelectedWorkspaceChanges(t *testing.T) {
	repo := initGitRepository(t)
	for name, content := range map[string]string{
		"selected.go": "package baseline\n",
		"outside.go":  "package baseline\n",
		"deleted.go":  "package baseline\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600))
		gitAdd(t, repo, name)
	}
	gitCommit(t, repo)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected.go"), []byte("package selected\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "outside.go"), []byte("package outside\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected_new.go"), []byte("package selected\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "outside_new.go"), []byte("package outside\n"), 0o600))
	require.NoError(t, os.Remove(filepath.Join(repo, "deleted.go")))
	gitAdd(t, repo, "selected.go")

	snapshot, cleanup, err := stageReviewSnapshotForPaths(
		context.Background(),
		repo,
		[]string{"selected.go", "selected_new.go", "deleted.go"},
		1024*1024,
	)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	data, err := os.ReadFile(filepath.Join(snapshot, "selected.go"))
	require.NoError(t, err)
	require.Equal(t, "package selected\n", string(data))
	data, err = os.ReadFile(filepath.Join(snapshot, "selected_new.go"))
	require.NoError(t, err)
	require.Equal(t, "package selected\n", string(data))
	_, err = os.Stat(filepath.Join(snapshot, "deleted.go"))
	require.ErrorIs(t, err, os.ErrNotExist)

	data, err = os.ReadFile(filepath.Join(snapshot, "outside.go"))
	require.NoError(t, err)
	require.Equal(t, "package baseline\n", string(data))
	_, err = os.Stat(filepath.Join(snapshot, "outside_new.go"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestContainerSandboxReusesImmutableSnapshotWithinReview(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected.go"), []byte("package baseline\n"), 0o600))
	gitAdd(t, repo, "selected.go")
	gitCommit(t, repo)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected.go"), []byte("package first\n"), 0o600))

	commit, err := resolveGitCommit(context.Background(), repo, "HEAD")
	require.NoError(t, err)
	pathspecs, err := literalGitPathspecs([]string{"selected.go"})
	require.NoError(t, err)
	diff, err := loadGitWorkspaceDiffAt(context.Background(), repo, pathspecs, commit)
	require.NoError(t, err)
	sum := sha256.Sum256(diff)
	scope := ReviewScope{
		FilePaths:  []string{"selected.go"},
		HeadCommit: commit,
		DiffSHA256: hex.EncodeToString(sum[:]),
	}
	sandbox := &ContainerSandbox{
		config:   DefaultSandboxConfig(),
		repoPath: repo,
	}
	t.Cleanup(sandbox.clearSnapshot)

	first, err := sandbox.snapshotForReview(context.Background(), "task-1", scope)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(first, "selected.go"))
	require.NoError(t, err)
	require.Equal(t, "package first\n", string(data))

	require.NoError(t, os.WriteFile(filepath.Join(repo, "selected.go"), []byte("package second\n"), 0o600))
	second, err := sandbox.snapshotForReview(context.Background(), "task-1", scope)
	require.NoError(t, err)
	require.Equal(t, first, second)
	data, err = os.ReadFile(filepath.Join(second, "selected.go"))
	require.NoError(t, err)
	require.Equal(t, "package first\n", string(data))

	_, err = sandbox.snapshotForReview(context.Background(), "task-2", scope)
	require.ErrorContains(t, err, "workspace changed")
}

func TestStageReviewSnapshotRejectsOversizedInput(t *testing.T) {
	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "large.go"), []byte("package large\n"), 0o600))
	gitAdd(t, repo, "large.go")
	gitCommit(t, repo)
	_, _, err := stageReviewSnapshot(context.Background(), repo, 4)
	require.ErrorContains(t, err, "exceeds")
}

func TestStageReviewSnapshotAppliesSelectedChangesBeforeSizeCheck(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, repo string)
	}{
		{
			name: "deletion",
			change: func(t *testing.T, repo string) {
				require.NoError(t, os.Remove(filepath.Join(repo, "large.go")))
			},
		},
		{
			name: "shrink",
			change: func(t *testing.T, repo string) {
				require.NoError(t, os.WriteFile(
					filepath.Join(repo, "large.go"),
					[]byte("x"),
					0o600,
				))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := initGitRepository(t)
			require.NoError(t, os.WriteFile(
				filepath.Join(repo, "large.go"),
				[]byte(strings.Repeat("x", 128)),
				0o600,
			))
			require.NoError(t, os.WriteFile(filepath.Join(repo, "keep.go"), []byte("keep"), 0o600))
			gitAdd(t, repo, "large.go", "keep.go")
			gitCommit(t, repo)
			test.change(t, repo)

			snapshot, cleanup, err := stageReviewSnapshotForPaths(
				context.Background(),
				repo,
				[]string{"large.go"},
				16,
			)
			require.NoError(t, err)
			t.Cleanup(cleanup)
			_, err = os.Stat(filepath.Join(snapshot, "keep.go"))
			require.NoError(t, err)
		})
	}
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
	gitCommit(t, repo)
	_, _, err := stageReviewSnapshot(context.Background(), repo, 1024*1024)
	require.ErrorContains(t, err, "not a regular file")
}

func TestStageReviewSnapshotAllowsSelectedSymlinkRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-dependent on Windows")
	}
	for _, test := range []struct {
		name        string
		replaceLink bool
	}{
		{name: "delete"},
		{name: "replace with regular file", replaceLink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := initGitRepository(t)
			require.NoError(t, os.WriteFile(
				filepath.Join(repo, "keep.go"),
				[]byte("package keep\n"),
				0o600,
			))
			link := filepath.Join(repo, "linked.go")
			require.NoError(t, os.Symlink("keep.go", link))
			gitAdd(t, repo, "keep.go", "linked.go")
			gitCommit(t, repo)
			require.NoError(t, os.Remove(link))
			if test.replaceLink {
				require.NoError(t, os.WriteFile(
					link,
					[]byte("package replacement\n"),
					0o600,
				))
			}

			snapshot, cleanup, err := stageReviewSnapshotForPaths(
				context.Background(),
				repo,
				[]string{"linked.go"},
				1024*1024,
			)
			require.NoError(t, err)
			t.Cleanup(cleanup)
			if test.replaceLink {
				info, err := os.Lstat(filepath.Join(snapshot, "linked.go"))
				require.NoError(t, err)
				require.True(t, info.Mode().IsRegular())
				data, err := os.ReadFile(filepath.Join(snapshot, "linked.go"))
				require.NoError(t, err)
				require.Equal(t, "package replacement\n", string(data))
			} else {
				_, err := os.Lstat(filepath.Join(snapshot, "linked.go"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
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
	require.ElementsMatch(t, []string{"ALL"}, []string(host.CapDrop))
	require.Contains(t, host.SecurityOpt, "no-new-privileges:true")
}

func TestModuleCacheRequiresExplicitTrustedMode(t *testing.T) {
	require.Equal(t, "/tmp/gomodcache", moduleCachePath(false))
	require.Equal(t, "/go/pkg/mod", moduleCachePath(true))
}

func TestDefaultDependencyEnvironmentFailsClosed(t *testing.T) {
	env := sandboxProgramEnv(false, false)
	require.Equal(t, "off", env["GOPROXY"])
	require.Equal(t, "/tmp/gomodcache", env["GOMODCACHE"])
	require.Equal(t, "-mod=readonly", env["GOFLAGS"])
	for _, value := range env {
		require.NotContains(t, value, "proxy.golang.org")
		require.NotContains(t, value, "sum.golang.org")
	}
}

func TestSandboxCleanupContextIgnoresParentCancellationButIsBounded(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()
	cleanupCtx, cleanupCancel := sandboxCleanupContext(parent)
	defer cleanupCancel()

	require.NoError(t, cleanupCtx.Err())
	deadline, ok := cleanupCtx.Deadline()
	require.True(t, ok)
	require.Positive(t, time.Until(deadline))
	require.LessOrEqual(t, time.Until(deadline), sandboxCleanupTimeout)
}

func TestContainerSandboxCloseWaitsForActiveExecution(t *testing.T) {
	sandbox := &ContainerSandbox{}
	sandbox.runMu.Lock()
	locked := true
	defer func() {
		if locked {
			sandbox.runMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- sandbox.Close() }()

	select {
	case err := <-done:
		require.Failf(t, "Close returned during active execution", "error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	sandbox.runMu.Unlock()
	locked = false
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "Close did not finish after execution released")
	}
}

func TestDefaultDependencySetupUsesVendoredSnapshot(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/reviewed\n\ngo 1.21\n\nrequire example.com/dependency v1.0.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "reviewed.go"), []byte("package reviewed\nimport \"example.com/dependency\"\nvar Value = dependency.Value\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "vendor", "example.com", "dependency"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "vendor", "modules.txt"), []byte("# example.com/dependency v1.0.0\n## explicit; go 1.21\nexample.com/dependency\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "vendor", "example.com", "dependency", "dependency.go"), []byte("package dependency\nconst Value = 1\n"), 0o600))

	module, vendor, err := dependencyPlan(repo, []string{"go", "test", "./..."}, false)
	require.NoError(t, err)
	require.Equal(t, ".", module)
	require.True(t, vendor)
	cmd := exec.Command("go", "test", "-mod=vendor", "./...")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	require.NoError(t, cmd.Run(), "vendored external dependency was not usable by the isolated default")
}

func TestDefaultDependencySetupPlansFailClosedPreparation(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/reviewed\n\ngo 1.21\n\nrequire example.com/dependency v1.0.0\n"), 0o600))

	module, vendor, err := dependencyPlan(repo, []string{"go", "test", "./..."}, false)
	require.NoError(t, err)
	require.Equal(t, ".", module)
	require.False(t, vendor)

	module, vendor, err = dependencyPlan(repo, []string{"go", "test", "./..."}, true)
	require.NoError(t, err)
	require.Empty(t, module)
	require.False(t, vendor)
}

func TestSandboxDockerfileUsesCompatibleNonRootImage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "skills", "code-review", "sandbox", "Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), "FROM golang:1.24")
	require.Contains(t, string(data), "USER reviewer:reviewer")
}

func TestContainerSandboxRunsWithUnprivilegedIdentity(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon is unavailable: %v", err)
	}

	repo := initGitRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("sandbox fixture\n"), 0o600))
	gitAdd(t, repo, "README.md")
	gitCommit(t, repo)
	dockerfilePath, err := filepath.Abs(filepath.Join("..", "skills", "code-review", "sandbox"))
	require.NoError(t, err)
	sandbox, err := NewContainerSandbox(DefaultSandboxConfig(), repo, dockerfilePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })

	uid := sandbox.Execute(context.Background(), "identity", "id -u", DecisionAllow, "integration test")
	require.Equal(t, SandboxStatusSuccess, uid.Status, "uid check failed: %+v", uid)
	require.Equal(t, "10001", strings.TrimSpace(uid.Stdout))

	capabilities := sandbox.Execute(
		context.Background(), "capabilities", "grep CapEff /proc/self/status", DecisionAllow, "integration test",
	)
	require.Equal(t, SandboxStatusSuccess, capabilities.Status, "capability check failed: %+v", capabilities)
	require.Equal(t, []string{"CapEff:", "0000000000000000"}, strings.Fields(capabilities.Stdout))

	noNewPrivileges := sandbox.Execute(
		context.Background(), "no-new-privileges", "grep NoNewPrivs /proc/self/status", DecisionAllow, "integration test",
	)
	require.Equal(t, SandboxStatusSuccess, noNewPrivileges.Status, "no-new-privileges check failed: %+v", noNewPrivileges)
	require.Equal(t, []string{"NoNewPrivs:", "1"}, strings.Fields(noNewPrivileges.Stdout))
}

func TestContainerSetupDeadlineIsReportedAsTimeout(t *testing.T) {
	repo := initGitRepository(t)
	closed := 0
	sandbox := &ContainerSandbox{
		config: withSandboxDefaults(SandboxConfig{}), repoPath: repo,
		closeExecutor: func() error { closed++; return nil },
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	run := sandbox.Execute(ctx, "expired-setup", "go test ./...", DecisionAllow, "")
	require.Equal(t, SandboxStatusTimeout, run.Status)
	require.True(t, run.TimedOut)
	require.Contains(t, run.Error, "during container setup")
	require.Equal(t, 1, closed)
}

func TestParentCancellationDuringSetupRecyclesContainer(t *testing.T) {
	repo := initGitRepository(t)
	closed := 0
	sandbox := &ContainerSandbox{
		config: withSandboxDefaults(SandboxConfig{}), repoPath: repo,
		closeExecutor: func() error { closed++; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := sandbox.Execute(ctx, "canceled-setup", "go test ./...", DecisionAllow, "")
	require.Equal(t, SandboxStatusError, run.Status)
	require.Contains(t, run.Error, "canceled")
	require.Equal(t, 1, closed)
}

func TestParentCancellationRecyclesContainer(t *testing.T) {
	closed := 0
	sandbox := &ContainerSandbox{
		config: withSandboxDefaults(SandboxConfig{}),
		closeExecutor: func() error {
			closed++
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &SandboxRun{}
	sandbox.finishExecution(ctx, run, codeexecutor.RunResult{}, context.Canceled)
	require.Equal(t, 1, closed)
	require.Equal(t, SandboxStatusError, run.Status)
	require.Contains(t, run.Error, "canceled")
	next := sandbox.Execute(context.Background(), "next", "go test ./...", DecisionAllow, "")
	require.Equal(t, SandboxStatusError, next.Status)
	require.Contains(t, next.Error, "recycled")
}
