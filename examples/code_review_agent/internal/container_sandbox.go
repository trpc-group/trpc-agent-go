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
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
}

// NewContainerSandbox starts the production container runtime.
func NewContainerSandbox(config SandboxConfig, repoPath, dockerfilePath string) (*ContainerSandbox, error) {
	options := []containerexecutor.Option{containerexecutor.WithDockerFilePath(dockerfilePath)}
	// The container has no network access. Reuse the host's already-downloaded
	// module cache as a read-only mount so checks remain deterministic without
	// allowing dependency downloads during review.
	if output, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil {
		if moduleCache := strings.TrimSpace(string(output)); moduleCache != "" {
			options = append(options, containerexecutor.WithBindMount(moduleCache, "/go/pkg/mod", "ro"))
		}
	}
	executor, err := containerexecutor.New(options...)
	if err != nil {
		return nil, fmt.Errorf("initialize container executor: %w", err)
	}
	return &ContainerSandbox{executor: executor, config: config, repoPath: repoPath}, nil
}

// Close releases the underlying container.
func (s *ContainerSandbox) Close() error { return s.executor.Close() }

// Execute runs one previously-authorized command.
func (s *ContainerSandbox) Execute(ctx context.Context, taskID, command string, decision Decision, reason string) *SandboxRun {
	run := &SandboxRun{ID: uuid.NewString(), TaskID: taskID, Command: command, PermissionDecision: decision, PermissionReason: reason, ExitCode: -1}
	if IsBlocked(decision) {
		run.Status = SandboxStatusBlocked
		run.Error = "command blocked by permission policy: " + reason
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
	ws, err := s.executor.CreateWorkspace(timeoutCtx, taskID+"-"+run.ID, codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: 256 * 1024 * 1024})
	if err != nil {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
		return run
	}
	defer s.executor.Cleanup(context.WithoutCancel(ctx), ws)
	if err := s.executor.PutDirectory(timeoutCtx, ws, s.repoPath, "repo"); err != nil {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
		return run
	}
	result, err := s.executor.RunProgram(timeoutCtx, ws, codeexecutor.RunProgramSpec{
		Cmd: parts[0], Args: parts[1:], Cwd: "repo", CleanEnv: true, Timeout: s.config.Timeout,
		Env: map[string]string{
			"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
			"GOPATH":      "/go",
			"GOMODCACHE":  "/go/pkg/mod",
			"GOCACHE":     "/tmp/go-build",
			"HOME":        "/tmp",
			"GOTOOLCHAIN": "local",
			"GOPROXY":     "off",
		},
		Limits: codeexecutor.ResourceLimits{CPUPercent: 200, MemoryMB: 1024, MaxPIDs: 256},
	})
	run.Stdout = RedactSensitiveInfo(truncateOutput(result.Stdout, s.config.MaxOutputBytes))
	run.Stderr = RedactSensitiveInfo(truncateOutput(result.Stderr, s.config.MaxOutputBytes))
	run.ExitCode, run.TimedOut = result.ExitCode, result.TimedOut
	if result.TimedOut || timeoutCtx.Err() == context.DeadlineExceeded {
		run.Status, run.TimedOut = SandboxStatusTimeout, true
		run.Error = fmt.Sprintf("command timed out after %s", s.config.Timeout)
	} else if err != nil {
		run.Status, run.Error = SandboxStatusError, RedactSensitiveInfo(err.Error())
	} else if result.ExitCode != 0 {
		run.Status, run.Error = SandboxStatusFailed, fmt.Sprintf("command exited with status %d", result.ExitCode)
	} else {
		run.Status = SandboxStatusSuccess
	}
	return run
}
