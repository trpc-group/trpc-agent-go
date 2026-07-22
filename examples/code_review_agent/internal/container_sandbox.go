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
	"path"
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
	executor      *containerexecutor.CodeExecutor
	config        SandboxConfig
	repoPath      string
	runMu         sync.Mutex
	mu            sync.Mutex
	closed        bool
	depMu         sync.Mutex
	depCache      string
	depReady      map[string]bool
	closeExecutor func() error
	goDownloadEnv func(moduleCache, buildCache, home string) []string
}

const (
	maxSnapshotPathListBytes = 4 * 1024 * 1024
	sandboxCleanupTimeout    = 5 * time.Second
)

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
	return &ContainerSandbox{executor: executor, config: config, repoPath: repoPath, closeExecutor: executor.Close}, nil
}

func hardenedHostConfig(config SandboxConfig) dockercontainer.HostConfig {
	pids := int64(config.MaxPIDs)
	return dockercontainer.HostConfig{
		AutoRemove:     true,
		Privileged:     false,
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
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

// Close releases the underlying container. It waits for an active Execute;
// authorized executions are serialized so one timeout cannot race a later
// command that has already started using the same container.
func (s *ContainerSandbox) Close() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.closeResources()
}

func (s *ContainerSandbox) closeResources() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var executorErr error
	if s.closeExecutor != nil {
		executorErr = s.closeExecutor()
	} else if s.executor != nil {
		executorErr = s.executor.Close()
	}
	s.depMu.Lock()
	defer s.depMu.Unlock()
	cacheErr := os.RemoveAll(s.depCache)
	s.depCache = ""
	s.depReady = nil
	return errors.Join(executorErr, cacheErr)
}

// Execute runs one previously-authorized command.
func (s *ContainerSandbox) Execute(ctx context.Context, taskID, command string, decision Decision, reason string) *SandboxRun {
	run := &SandboxRun{ID: uuid.NewString(), TaskID: taskID, Command: command, PermissionDecision: decision, PermissionReason: reason, ExitCode: -1}
	if IsBlocked(decision) {
		run.Status = SandboxStatusBlocked
		run.Error = "command blocked by permission policy: " + reason
		return run
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		run.Status, run.Error = SandboxStatusError, "container was recycled after a previous terminated execution"
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
		s.classifyContainerSetupError(run, timeoutCtx, err)
		return run
	}
	defer removeSnapshot()
	dependencyCache, useVendor, err := s.prepareDependencies(timeoutCtx, snapshotPath, parts)
	if err != nil {
		s.classifyContainerSetupError(run, timeoutCtx, err)
		return run
	}
	ws, err := s.executor.CreateWorkspace(timeoutCtx, taskID+"-"+run.ID, codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: s.config.MaxWorkspaceBytes})
	if err != nil {
		s.classifyContainerSetupError(run, timeoutCtx, err)
		return run
	}
	defer func() {
		cleanupCtx, cleanupCancel := sandboxCleanupContext(ctx)
		defer cleanupCancel()
		_ = s.executor.Cleanup(cleanupCtx, ws)
	}()
	if err := s.executor.PutDirectory(timeoutCtx, ws, snapshotPath, "repo"); err != nil {
		s.classifyContainerSetupError(run, timeoutCtx, err)
		return run
	}
	gomodcache := moduleCachePath(s.config.TrustedModuleCache)
	if dependencyCache != "" {
		if err := s.executor.PutDirectory(timeoutCtx, ws, dependencyCache, "gomodcache"); err != nil {
			s.classifyContainerSetupError(run, timeoutCtx, fmt.Errorf("stage isolated module cache: %w", err))
			return run
		}
		gomodcache = path.Join(ws.Path, "gomodcache")
	}
	programEnv := map[string]string{
		"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"GOPATH":      "/go",
		"GOMODCACHE":  gomodcache,
		"GOCACHE":     "/tmp/go-build",
		"HOME":        "/tmp",
		"GOTOOLCHAIN": "local",
		"GOPROXY":     "off",
		"GOFLAGS":     "-mod=readonly",
	}
	if useVendor {
		programEnv["GOFLAGS"] = "-mod=vendor"
	}
	result, err := s.executor.RunProgram(timeoutCtx, ws, codeexecutor.RunProgramSpec{
		Cmd: parts[0], Args: parts[1:], Cwd: "repo", CleanEnv: true, Timeout: s.config.Timeout,
		Env:            programEnv,
		Limits:         codeexecutor.ResourceLimits{CPUPercent: s.config.CPUPercent, MemoryMB: s.config.MemoryMB, MaxPIDs: s.config.MaxPIDs},
		MaxOutputBytes: s.config.MaxOutputBytes,
	})
	s.finishExecution(timeoutCtx, run, result, err)
	return run
}

func sandboxCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), sandboxCleanupTimeout)
}

func (s *ContainerSandbox) finishExecution(timeoutCtx context.Context, run *SandboxRun, result codeexecutor.RunResult, err error) {
	run.Stdout = RedactSensitiveInfo(result.Stdout)
	run.Stderr = RedactSensitiveInfo(result.Stderr)
	run.ExitCode, run.TimedOut = result.ExitCode, result.TimedOut
	if result.TimedOut || timeoutCtx.Err() != nil {
		// Docker exec cannot reliably kill descendants. Recycle the entire
		// container for both deadline expiry and parent cancellation before a
		// later check can observe a surviving process.
		closeErr := s.closeResources()
		if result.TimedOut || errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			run.Status, run.TimedOut = SandboxStatusTimeout, true
			run.Error = fmt.Sprintf("command timed out after %s", s.config.Timeout)
		} else {
			run.Status = SandboxStatusError
			run.Error = "command canceled by parent context"
		}
		if closeErr != nil {
			run.Error += ": recycle container: " + RedactSensitiveInfo(closeErr.Error())
		}
	} else if err != nil {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
	} else if result.ExitCode != 0 {
		run.Status, run.Error = SandboxStatusFailed, fmt.Sprintf("command exited with status %d", result.ExitCode)
	} else {
		run.Status = SandboxStatusSuccess
	}
}

func (s *ContainerSandbox) classifyContainerSetupError(run *SandboxRun, timeoutCtx context.Context, err error) {
	terminated := timeoutCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var closeErr error
	if terminated {
		closeErr = s.closeResources()
	}
	if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		run.Status, run.TimedOut = SandboxStatusTimeout, true
		run.Error = fmt.Sprintf("command timed out after %s during container setup", s.config.Timeout)
	} else if terminated {
		run.Status = SandboxStatusError
		run.Error = "container setup canceled by parent context"
	} else {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
	}
	if closeErr != nil {
		run.Error += ": recycle container: " + RedactSensitiveInfo(closeErr.Error())
	}
}

func moduleCachePath(trusted bool) string {
	if trusted {
		return "/go/pkg/mod"
	}
	return "/tmp/gomodcache"
}

func (s *ContainerSandbox) prepareDependencies(ctx context.Context, snapshotRoot string, command []string) (string, bool, error) {
	if s.config.TrustedModuleCache || len(command) == 0 || command[0] != "go" {
		return "", false, nil
	}
	module := "."
	for i := 1; i < len(command); i++ {
		if command[i] == "-C" && i+1 < len(command) {
			module = command[i+1]
			break
		}
		if strings.HasPrefix(command[i], "-C=") {
			module = strings.TrimPrefix(command[i], "-C=")
			break
		}
	}
	module = filepath.Clean(filepath.FromSlash(module))
	if filepath.IsAbs(module) || module == ".." || strings.HasPrefix(module, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("unsafe Go module directory %q", module)
	}
	moduleDir := filepath.Join(snapshotRoot, module)
	moduleDir, err := filepath.Abs(moduleDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve Go module directory %q: %w", module, err)
	}
	moduleDir, err = filepath.EvalSymlinks(moduleDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve Go module directory links %q: %w", module, err)
	}
	inside, err := pathInside(snapshotRoot, moduleDir)
	if err != nil || !inside {
		return "", false, fmt.Errorf("Go module directory %q escapes repository", module)
	}
	if info, err := os.Stat(filepath.Join(moduleDir, "vendor", "modules.txt")); err == nil && !info.IsDir() {
		return "", true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect vendored dependencies for %q: %w", module, err)
	}

	s.depMu.Lock()
	defer s.depMu.Unlock()
	if s.depReady != nil && s.depReady[module] {
		return s.depCache, false, nil
	}
	if s.depCache == "" {
		s.depCache, err = os.MkdirTemp("", "code-review-gomodcache-")
		if err != nil {
			return "", false, fmt.Errorf("create isolated module cache: %w", err)
		}
		s.depReady = map[string]bool{}
	}
	home := filepath.Join(s.depCache, ".home")
	buildCache := filepath.Join(s.depCache, ".build")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", false, fmt.Errorf("create isolated Go home: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "all")
	cmd.Dir = moduleDir
	envBuilder := s.goDownloadEnv
	if envBuilder == nil {
		envBuilder = isolatedGoDownloadEnv
	}
	cmd.Env = envBuilder(s.depCache, buildCache, home)
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 64*1024, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("prepare isolated dependencies for module %q: %w: %s", module, err, strings.TrimSpace(stderr.String()))
	}
	s.depReady[module] = true
	return s.depCache, false, nil
}

func isolatedGoDownloadEnv(moduleCache, buildCache, home string) []string {
	return isolatedGoDownloadEnvForProxy(moduleCache, buildCache, home, "https://proxy.golang.org")
}

func isolatedGoDownloadEnvForProxy(moduleCache, buildCache, home, proxy string) []string {
	env := []string{
		"GOMODCACHE=" + moduleCache,
		"GOCACHE=" + buildCache,
		"HOME=" + home,
		"GOENV=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=" + proxy,
		"GOSUMDB=sum.golang.org",
		"GOPRIVATE=",
		"GONOPROXY=none",
		"GONOSUMDB=",
	}
	for _, key := range []string{"PATH", "SYSTEMROOT", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
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
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
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
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
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
