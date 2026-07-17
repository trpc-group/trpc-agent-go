//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexecutor "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

// ContainerSandbox executes checks in a network-disabled, unprivileged
// codeexecutor/container workspace. The repository is copied into an isolated
// workspace instead of being modified in place.
type ContainerSandbox struct {
	executor *containerexecutor.CodeExecutor
	config   SandboxConfig
	repoPath string
	mu       sync.Mutex
	closed   bool
}

const maxSnapshotPathListBytes = 4 * 1024 * 1024

// NewContainerSandbox starts the production container runtime.
func NewContainerSandbox(config SandboxConfig, repoPath, dockerfilePath string) (*ContainerSandbox, error) {
	config = withSandboxDefaults(config)
	hostConfig := hardenedHostConfig(config)
	options := []containerexecutor.Option{
		containerexecutor.WithDockerFilePath(dockerfilePath),
		containerexecutor.WithHostConfig(hostConfig),
	}
	// A broad host module cache can contain unrelated private source. It is
	// available only as an explicit trusted-mode opt-in.
	if config.TrustedModuleCache {
		output, err := exec.Command("go", "env", "GOMODCACHE").Output()
		if err != nil {
			return nil, fmt.Errorf("resolve trusted module cache: %w", err)
		}
		moduleCache := strings.TrimSpace(string(output))
		if moduleCache == "" {
			return nil, errors.New("trusted module cache path is empty")
		}
		options = append(options, containerexecutor.WithBindMount(moduleCache, "/go/pkg/mod", "ro"))
	}
	executor, err := containerexecutor.New(options...)
	if err != nil {
		return nil, fmt.Errorf("initialize container executor: %w", err)
	}
	return &ContainerSandbox{executor: executor, config: config, repoPath: repoPath}, nil
}

func hardenedHostConfig(config SandboxConfig) dockercontainer.HostConfig {
	pids := int64(config.MaxPIDs)
	return dockercontainer.HostConfig{
		AutoRemove:     true,
		Privileged:     false,
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp": fmt.Sprintf("rw,nosuid,nodev,size=%d", config.MaxWorkspaceBytes),
		},
		Resources: dockercontainer.Resources{
			NanoCPUs:  int64(config.CPUPercent) * 10_000_000,
			Memory:    int64(config.MemoryMB) * 1024 * 1024,
			PidsLimit: &pids,
		},
	}
}

// Close releases the underlying container.
func (s *ContainerSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.executor.Close()
}

// Execute runs one previously-authorized command.
func (s *ContainerSandbox) Execute(ctx context.Context, taskID, command string, decision Decision, reason string) *SandboxRun {
	run := &SandboxRun{ID: uuid.NewString(), TaskID: taskID, Command: command, PermissionDecision: decision, PermissionReason: reason, ExitCode: -1}
	if IsBlocked(decision) {
		run.Status = SandboxStatusBlocked
		run.Error = "command blocked by permission policy: " + reason
		return run
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		run.Status, run.Error = SandboxStatusError, "container was recycled after a previous timeout"
		return run
	}
	start := time.Now()
	defer func() { run.Duration = time.Since(start) }()
	parts := strings.Fields(command)
	if len(parts) == 0 {
		run.Status, run.Error = SandboxStatusError, "empty command"
		return run
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	snapshotPath, removeSnapshot, err := stageReviewSnapshot(timeoutCtx, s.repoPath, s.config.MaxWorkspaceBytes)
	if err != nil {
		classifyContainerSetupError(run, timeoutCtx, s.config.Timeout, err)
		return run
	}
	defer removeSnapshot()
	ws, err := s.executor.CreateWorkspace(timeoutCtx, taskID+"-"+run.ID, codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: s.config.MaxWorkspaceBytes})
	if err != nil {
		classifyContainerSetupError(run, timeoutCtx, s.config.Timeout, err)
		return run
	}
	defer s.executor.Cleanup(context.WithoutCancel(ctx), ws)
	if err := s.executor.PutDirectory(timeoutCtx, ws, snapshotPath, "repo"); err != nil {
		classifyContainerSetupError(run, timeoutCtx, s.config.Timeout, err)
		return run
	}
	result, err := s.executor.RunProgram(timeoutCtx, ws, codeexecutor.RunProgramSpec{
		Cmd: parts[0], Args: parts[1:], Cwd: "repo", CleanEnv: true, Timeout: s.config.Timeout,
		Env: map[string]string{
			"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
			"GOPATH":      "/go",
			"GOMODCACHE":  moduleCachePath(s.config.TrustedModuleCache),
			"GOCACHE":     "/tmp/go-build",
			"HOME":        "/tmp",
			"GOTOOLCHAIN": "local",
			"GOPROXY":     "off",
		},
		Limits:         codeexecutor.ResourceLimits{CPUPercent: s.config.CPUPercent, MemoryMB: s.config.MemoryMB, MaxPIDs: s.config.MaxPIDs},
		MaxOutputBytes: s.config.MaxOutputBytes,
	})
	run.Stdout = RedactSensitiveInfo(result.Stdout)
	run.Stderr = RedactSensitiveInfo(result.Stderr)
	run.ExitCode, run.TimedOut = result.ExitCode, result.TimedOut
	if result.TimedOut || timeoutCtx.Err() == context.DeadlineExceeded {
		run.Status, run.TimedOut = SandboxStatusTimeout, true
		run.Error = fmt.Sprintf("command timed out after %s", s.config.Timeout)
		// Docker exec cannot reliably kill descendants. Closing the executor
		// recycles the whole container before another command can start.
		_ = s.Close()
	} else if err != nil {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
	} else if result.ExitCode != 0 {
		run.Status, run.Error = SandboxStatusFailed, fmt.Sprintf("command exited with status %d", result.ExitCode)
	} else {
		run.Status = SandboxStatusSuccess
	}
	return run
}

func classifyContainerSetupError(run *SandboxRun, timeoutCtx context.Context, timeout time.Duration, err error) {
	if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		run.Status, run.TimedOut = SandboxStatusTimeout, true
		run.Error = fmt.Sprintf("command timed out after %s during container setup", timeout)
		return
	}
	run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
}

func moduleCachePath(trusted bool) string {
	if trusted {
		return "/go/pkg/mod"
	}
	return "/tmp/gomodcache"
}

func stageReviewSnapshot(ctx context.Context, root string, limit int64) (string, func(), error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve repository links: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--")
	cmd.Dir = root
	cmd.Env = append(filteredGitEnv(), "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxSnapshotPathListBytes, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", func() {}, fmt.Errorf("list review snapshot files: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return "", func() {}, fmt.Errorf("review snapshot path list exceeds %d bytes", maxSnapshotPathListBytes)
	}
	snapshot, err := os.MkdirTemp("", "code-review-snapshot-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create review snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	var total int64
	for _, rawName := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(string(rawName)))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || name == ".git" || strings.HasPrefix(name, ".git"+string(filepath.Separator)) {
			cleanup()
			return "", func() {}, fmt.Errorf("unsafe review snapshot path %q", name)
		}
		source := filepath.Join(root, name)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue // tracked deletion
		}
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("lstat review file %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			cleanup()
			return "", func() {}, fmt.Errorf("review file %q is not a regular file", name)
		}
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("resolve review file %q: %w", name, err)
		}
		inside, err := pathInside(root, resolved)
		if err != nil || !inside {
			cleanup()
			return "", func() {}, fmt.Errorf("review file %q resolves outside repository", name)
		}
		if info.Size() > limit-total {
			cleanup()
			return "", func() {}, fmt.Errorf("review snapshot exceeds %d-byte limit", limit)
		}
		destination := filepath.Join(snapshot, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("create snapshot directory for %q: %w", name, err)
		}
		sourceFile, err := os.Open(source)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("open review file %q: %w", name, err)
		}
		openedInfo, statErr := sourceFile.Stat()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = sourceFile.Close()
			cleanup()
			return "", func() {}, fmt.Errorf("review file %q changed while opening", name)
		}
		mode := os.FileMode(0o600)
		if info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = sourceFile.Close()
			cleanup()
			return "", func() {}, fmt.Errorf("create snapshot file %q: %w", name, err)
		}
		written, copyErr := io.Copy(destinationFile, io.LimitReader(sourceFile, limit-total+1))
		closeSourceErr := sourceFile.Close()
		closeDestinationErr := destinationFile.Close()
		if written > limit-total {
			cleanup()
			return "", func() {}, fmt.Errorf("review snapshot exceeds %d-byte limit while copying %q", limit, name)
		}
		if copyErr != nil || closeSourceErr != nil || closeDestinationErr != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("copy review file %q within limit: %w", name, errors.Join(copyErr, closeSourceErr, closeDestinationErr))
		}
		total += written
	}
	return snapshot, cleanup, nil
}
