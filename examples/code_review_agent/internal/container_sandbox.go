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
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	executor       *containerexecutor.CodeExecutor
	config         SandboxConfig
	repoPath       string
	runMu          sync.Mutex
	mu             sync.Mutex
	closed         bool
	closeExecutor  func() error
	snapshotTask   string
	snapshotScope  ReviewScope
	snapshotPath   string
	removeSnapshot func()
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
	s.clearSnapshot()
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
	return executorErr
}

func (s *ContainerSandbox) clearSnapshot() {
	if s.removeSnapshot != nil {
		s.removeSnapshot()
	}
	s.snapshotTask = ""
	s.snapshotScope = ReviewScope{}
	s.snapshotPath = ""
	s.removeSnapshot = nil
}

func (s *ContainerSandbox) snapshotForReview(
	ctx context.Context,
	taskID string,
	scope ReviewScope,
) (string, error) {
	if s.snapshotPath != "" &&
		s.snapshotTask == taskID &&
		equalReviewScope(s.snapshotScope, scope) {
		return s.snapshotPath, nil
	}
	s.clearSnapshot()
	path, cleanup, err := stageReviewSnapshotForScope(
		ctx,
		s.repoPath,
		scope,
		s.config.MaxWorkspaceBytes,
	)
	if err != nil {
		return "", err
	}
	s.snapshotTask = taskID
	s.snapshotScope = ReviewScope{
		FilePaths:  append([]string(nil), scope.FilePaths...),
		HeadCommit: scope.HeadCommit,
		DiffSHA256: scope.DiffSHA256,
	}
	s.snapshotPath = path
	s.removeSnapshot = cleanup
	return path, nil
}

func equalReviewScope(left, right ReviewScope) bool {
	if left.HeadCommit != right.HeadCommit ||
		left.DiffSHA256 != right.DiffSHA256 ||
		len(left.FilePaths) != len(right.FilePaths) {
		return false
	}
	for index := range left.FilePaths {
		if left.FilePaths[index] != right.FilePaths[index] {
			return false
		}
	}
	return true
}

// Execute runs one previously-authorized command.
func (s *ContainerSandbox) Execute(ctx context.Context, taskID, command string, decision Decision, reason string) *SandboxRun {
	return s.execute(ctx, taskID, command, decision, reason, ReviewScope{})
}

// ExecuteReview runs one previously-authorized command against a snapshot
// containing HEAD plus only the workspace changes selected for this review.
func (s *ContainerSandbox) ExecuteReview(
	ctx context.Context,
	taskID string,
	command string,
	decision Decision,
	reason string,
	scope ReviewScope,
) *SandboxRun {
	return s.execute(ctx, taskID, command, decision, reason, scope)
}

func (s *ContainerSandbox) execute(
	ctx context.Context,
	taskID string,
	command string,
	decision Decision,
	reason string,
	scope ReviewScope,
) *SandboxRun {
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
	snapshotPath, err := s.snapshotForReview(
		timeoutCtx,
		taskID,
		scope,
	)
	if err != nil {
		s.classifyContainerSetupError(run, timeoutCtx, err)
		return run
	}
	module, useVendor, err := dependencyPlan(snapshotPath, parts, s.config.TrustedModuleCache)
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
	programEnv := sandboxProgramEnv(s.config.TrustedModuleCache, useVendor)
	if module != "" && !useVendor {
		dependencyResult, dependencyErr := s.executor.RunProgram(
			timeoutCtx,
			ws,
			codeexecutor.RunProgramSpec{
				Cmd: "go", Args: []string{"-C", module, "mod", "download", "all"}, Cwd: "repo",
				CleanEnv: true, Timeout: s.config.Timeout, Env: programEnv,
				Limits: codeexecutor.ResourceLimits{
					CPUPercent: s.config.CPUPercent,
					MemoryMB:   s.config.MemoryMB,
					MaxPIDs:    s.config.MaxPIDs,
				},
				MaxOutputBytes: s.config.MaxOutputBytes,
			},
		)
		if dependencyResult.TimedOut {
			s.finishExecution(timeoutCtx, run, dependencyResult, dependencyErr)
			return run
		}
		if dependencyErr != nil {
			s.classifyContainerSetupError(
				run, timeoutCtx, fmt.Errorf("prepare dependencies inside sandbox: %w", dependencyErr),
			)
			return run
		}
		if dependencyResult.ExitCode != 0 {
			s.classifyContainerSetupError(
				run,
				timeoutCtx,
				fmt.Errorf(
					"prepare dependencies inside sandbox: go exited with status %d: %s",
					dependencyResult.ExitCode,
					strings.TrimSpace(dependencyResult.Stderr),
				),
			)
			return run
		}
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

func sandboxProgramEnv(trustedModuleCache, useVendor bool) map[string]string {
	programEnv := map[string]string{
		"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"GOPATH":      "/go",
		"GOMODCACHE":  moduleCachePath(trustedModuleCache),
		"GOCACHE":     "/tmp/go-build",
		"HOME":        "/tmp",
		"GOTOOLCHAIN": "local",
		"GOPROXY":     "off",
		"GOFLAGS":     "-mod=readonly",
	}
	if useVendor {
		programEnv["GOFLAGS"] = "-mod=vendor"
	}
	return programEnv
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

func dependencyPlan(snapshotRoot string, command []string, trustedModuleCache bool) (string, bool, error) {
	if trustedModuleCache || len(command) == 0 || command[0] != "go" {
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
	if info, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err == nil && !info.IsDir() {
		// Continue below to select vendor mode or an isolated download.
	} else if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("inspect Go module file for %q: %w", module, err)
	} else {
		return "", false, fmt.Errorf("Go module file for %q is not a regular file", module)
	}
	if info, err := os.Stat(filepath.Join(moduleDir, "vendor", "modules.txt")); err == nil && !info.IsDir() {
		return filepath.ToSlash(module), true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect vendored dependencies for %q: %w", module, err)
	}
	return filepath.ToSlash(module), false, nil
}

type snapshotTreeEntry struct {
	path     string
	objectID string
	size     int64
	mode     os.FileMode
	regular  bool
}

type snapshotWorkspaceChange struct {
	path   string
	delete bool
}

func stageReviewSnapshot(ctx context.Context, root string, limit int64) (string, func(), error) {
	return stageReviewSnapshotForScope(ctx, root, ReviewScope{}, limit)
}

func stageReviewSnapshotForPaths(
	ctx context.Context,
	root string,
	filePaths []string,
	limit int64,
) (string, func(), error) {
	return stageReviewSnapshotForScope(
		ctx,
		root,
		ReviewScope{FilePaths: append([]string(nil), filePaths...)},
		limit,
	)
}

func stageReviewSnapshotForScope(
	ctx context.Context,
	root string,
	scope ReviewScope,
	limit int64,
) (string, func(), error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve repository links: %w", err)
	}
	commitRef := scope.HeadCommit
	if commitRef == "" {
		commitRef = "HEAD"
	}
	commit, err := resolveGitCommit(ctx, root, commitRef)
	if err != nil {
		return "", func() {}, err
	}
	pathspecs, err := literalGitPathspecs(scope.FilePaths)
	if err != nil {
		return "", func() {}, err
	}
	if err := validateReviewDiff(
		ctx,
		root,
		pathspecs,
		commit,
		scope.DiffSHA256,
	); err != nil {
		return "", func() {}, err
	}
	changes, err := reviewWorkspaceChanges(ctx, root, commit, pathspecs)
	if err != nil {
		return "", func() {}, err
	}
	entries, err := snapshotCommitEntries(ctx, root, commit)
	if err != nil {
		return "", func() {}, err
	}
	entries, sizes, total, err := excludeChangedSnapshotEntries(entries, changes)
	if err != nil {
		return "", func() {}, err
	}
	if total > limit {
		return "", func() {}, fmt.Errorf("review snapshot exceeds %d-byte limit", limit)
	}
	snapshot, err := os.MkdirTemp("", "code-review-snapshot-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create review snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	if err := materializeHEADSnapshot(ctx, root, snapshot, entries); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if _, err := overlayReviewWorkspace(
		root,
		snapshot,
		changes,
		limit,
		total,
		sizes,
	); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := validateReviewDiff(
		ctx,
		root,
		pathspecs,
		commit,
		scope.DiffSHA256,
	); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return snapshot, cleanup, nil
}

func validateReviewDiff(
	ctx context.Context,
	root string,
	pathspecs []string,
	commit string,
	expectedHash string,
) error {
	if expectedHash == "" {
		return nil
	}
	expected, err := hex.DecodeString(expectedHash)
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("invalid expected review diff hash")
	}
	diff, err := loadGitWorkspaceDiffAt(ctx, root, pathspecs, commit)
	if err != nil {
		return fmt.Errorf("verify review workspace diff: %w", err)
	}
	actual := sha256.Sum256(diff)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("review workspace changed after its diff was captured")
	}
	return nil
}

func snapshotCommitEntries(
	ctx context.Context,
	root string,
	commit string,
) ([]snapshotTreeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-tree", "-r", "-z", "--long", "--full-tree", commit)
	cmd.Dir = root
	cmd.Env = isolatedGitEnv()
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxSnapshotPathListBytes, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf(
			"list commit snapshot files: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if stdout.exceeded {
		return nil, fmt.Errorf(
			"commit snapshot path list exceeds %d bytes",
			maxSnapshotPathListBytes,
		)
	}
	records := bytes.Split(stdout.Bytes(), []byte{0})
	entries := make([]snapshotTreeEntry, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		metadata, rawName, ok := bytes.Cut(record, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("malformed commit tree entry")
		}
		fields := bytes.Fields(metadata)
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed commit tree metadata for %q", rawName)
		}
		gitMode, objectType, objectID := string(fields[0]), string(fields[1]), string(fields[2])
		name, err := safeSnapshotPath(string(rawName))
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate review snapshot path %q", name)
		}
		seen[name] = true
		fileMode := os.FileMode(0o644)
		regular := objectType == "blob" && (gitMode == "100644" || gitMode == "100755")
		var size int64
		if regular {
			size, err = strconv.ParseInt(string(fields[3]), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse commit size for %q: %w", rawName, err)
			}
			if size < 0 {
				return nil, fmt.Errorf("commit size for %q is negative", rawName)
			}
		}
		if gitMode == "100755" {
			fileMode = 0o755
		}
		entries = append(entries, snapshotTreeEntry{
			path:     name,
			objectID: objectID,
			size:     size,
			mode:     fileMode,
			regular:  regular,
		})
	}
	return entries, nil
}

func excludeChangedSnapshotEntries(
	entries []snapshotTreeEntry,
	changes []snapshotWorkspaceChange,
) ([]snapshotTreeEntry, map[string]int64, int64, error) {
	changed := make(map[string]bool, len(changes))
	for _, change := range changes {
		changed[change.path] = true
	}
	filtered := make([]snapshotTreeEntry, 0, len(entries))
	sizes := make(map[string]int64, len(entries))
	var total int64
	for _, entry := range entries {
		if changed[entry.path] {
			continue
		}
		if !entry.regular {
			return nil, nil, 0, fmt.Errorf(
				"review file %q is not a regular file",
				entry.path,
			)
		}
		if entry.size > int64(^uint64(0)>>1)-total {
			return nil, nil, 0, fmt.Errorf("review snapshot size overflows")
		}
		filtered = append(filtered, entry)
		sizes[entry.path] = entry.size
		total += entry.size
	}
	return filtered, sizes, total, nil
}

func materializeHEADSnapshot(
	ctx context.Context,
	root string,
	snapshot string,
	entries []snapshotTreeEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	var input strings.Builder
	for _, entry := range entries {
		input.WriteString(entry.objectID)
		input.WriteByte('\n')
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = root
	cmd.Env = isolatedGitEnv()
	cmd.Stdin = strings.NewReader(input.String())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open HEAD object stream: %w", err)
	}
	var stderr limitedBuffer
	stderr.limit = 64 * 1024
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start HEAD object stream: %w", err)
	}
	abort := func(cause error) error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return cause
	}
	reader := bufio.NewReader(stdout)
	for _, entry := range entries {
		header, err := reader.ReadString('\n')
		if err != nil {
			return abort(fmt.Errorf("read HEAD object header for %q: %w", entry.path, err))
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != entry.objectID || fields[1] != "blob" {
			return abort(fmt.Errorf("unexpected HEAD object header for %q", entry.path))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size != entry.size {
			return abort(fmt.Errorf("unexpected HEAD object size for %q", entry.path))
		}
		destination := filepath.Join(snapshot, entry.path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return abort(fmt.Errorf("create snapshot directory for %q: %w", entry.path, err))
		}
		destinationFile, err := os.OpenFile(
			destination,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			entry.mode,
		)
		if err != nil {
			return abort(fmt.Errorf("create HEAD snapshot file %q: %w", entry.path, err))
		}
		written, copyErr := io.CopyN(destinationFile, reader, entry.size)
		terminator, terminatorErr := reader.ReadByte()
		closeErr := destinationFile.Close()
		if copyErr != nil || written != entry.size || terminatorErr != nil || terminator != '\n' || closeErr != nil {
			streamErr := errors.Join(copyErr, terminatorErr, closeErr)
			if written != entry.size {
				streamErr = errors.Join(streamErr, io.ErrUnexpectedEOF)
			}
			if terminatorErr == nil && terminator != '\n' {
				streamErr = errors.Join(streamErr, errors.New("invalid object terminator"))
			}
			return abort(fmt.Errorf(
				"materialize HEAD snapshot file %q: %w",
				entry.path,
				streamErr,
			))
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf(
			"read HEAD snapshot objects: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return nil
}

func overlayReviewWorkspace(
	root string,
	snapshot string,
	changes []snapshotWorkspaceChange,
	limit int64,
	total int64,
	sizes map[string]int64,
) (int64, error) {
	for _, change := range changes {
		if !change.delete {
			continue
		}
		previous := sizes[change.path]
		if err := os.Remove(filepath.Join(snapshot, change.path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return total, fmt.Errorf("remove deleted review file %q: %w", change.path, err)
		}
		delete(sizes, change.path)
		total -= previous
	}
	for _, change := range changes {
		if change.delete {
			continue
		}
		var err error
		total, err = copyWorkspaceFile(root, snapshot, change.path, limit, total, sizes)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func reviewWorkspaceChanges(
	ctx context.Context,
	root string,
	commit string,
	pathspecs []string,
) ([]snapshotWorkspaceChange, error) {
	view, cleanup, err := newIsolatedGitView(ctx, root, commit)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args := []string{
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
		"-z",
		"--",
	}
	args = append(args, pathspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = view.env
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxSnapshotPathListBytes, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf(
			"list selected workspace files: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if stdout.exceeded {
		return nil, fmt.Errorf(
			"selected workspace file list exceeds %d-byte limit",
			maxSnapshotPathListBytes,
		)
	}
	seen := make(map[string]bool)
	var paths []string
	for _, rawName := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		name, err := safeSnapshotPath(string(rawName))
		if err != nil {
			return nil, err
		}
		if !seen[name] {
			seen[name] = true
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)
	changes := make([]snapshotWorkspaceChange, 0, len(paths))
	for _, name := range paths {
		_, err := os.Lstat(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			changes = append(changes, snapshotWorkspaceChange{path: name, delete: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("lstat selected workspace file %q: %w", name, err)
		}
		changes = append(changes, snapshotWorkspaceChange{path: name})
	}
	return changes, nil
}

func runSnapshotGitList(
	ctx context.Context,
	root string,
	args []string,
	operation string,
) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = isolatedGitEnv()
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxSnapshotPathListBytes, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", operation, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("%s exceeds %d-byte limit", operation, maxSnapshotPathListBytes)
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func safeSnapshotPath(rawName string) (string, error) {
	name := filepath.Clean(filepath.FromSlash(rawName))
	if name == "." ||
		filepath.IsAbs(name) ||
		name == ".." ||
		strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
		name == ".git" ||
		strings.HasPrefix(name, ".git"+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe review snapshot path %q", rawName)
	}
	return name, nil
}

func copyWorkspaceFile(
	root string,
	snapshot string,
	name string,
	limit int64,
	total int64,
	sizes map[string]int64,
) (int64, error) {
	source := filepath.Join(root, name)
	info, err := os.Lstat(source)
	if err != nil {
		return total, fmt.Errorf("lstat review file %q: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return total, fmt.Errorf("review file %q is not a regular file", name)
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return total, fmt.Errorf("resolve review file %q: %w", name, err)
	}
	inside, err := pathInside(root, resolved)
	if err != nil || !inside {
		return total, fmt.Errorf("review file %q resolves outside repository", name)
	}
	baseTotal := total - sizes[name]
	if info.Size() > limit-baseTotal {
		return total, fmt.Errorf("review snapshot exceeds %d-byte limit", limit)
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return total, fmt.Errorf("open review file %q: %w", name, err)
	}
	openedInfo, statErr := sourceFile.Stat()
	if statErr != nil || !os.SameFile(info, openedInfo) {
		_ = sourceFile.Close()
		return total, fmt.Errorf("review file %q changed while opening", name)
	}
	destination := filepath.Join(snapshot, name)
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = sourceFile.Close()
		return total, fmt.Errorf("replace review file %q: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		_ = sourceFile.Close()
		return total, fmt.Errorf("create snapshot directory for %q: %w", name, err)
	}
	mode := os.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	destinationFile, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		mode,
	)
	if err != nil {
		_ = sourceFile.Close()
		return total, fmt.Errorf("create snapshot file %q: %w", name, err)
	}
	written, copyErr := io.Copy(
		destinationFile,
		io.LimitReader(sourceFile, limit-baseTotal+1),
	)
	closeSourceErr := sourceFile.Close()
	closeDestinationErr := destinationFile.Close()
	if written > limit-baseTotal {
		_ = os.Remove(destination)
		return total, fmt.Errorf(
			"review snapshot exceeds %d-byte limit while copying %q",
			limit,
			name,
		)
	}
	if copyErr != nil || closeSourceErr != nil || closeDestinationErr != nil {
		_ = os.Remove(destination)
		return total, fmt.Errorf(
			"copy review file %q within limit: %w",
			name,
			errors.Join(copyErr, closeSourceErr, closeDestinationErr),
		)
	}
	sizes[name] = written
	return baseTotal + written, nil
}
