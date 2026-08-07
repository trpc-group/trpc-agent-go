//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandbox manages sandboxed execution of review checks.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

// RuntimeType specifies the sandbox runtime to use.
type RuntimeType string

// RuntimeType values.
const (
	RuntimeLocal     RuntimeType = "local"
	RuntimeContainer RuntimeType = "container"
	RuntimeFake      RuntimeType = "fake"
)

const defaultContainerImage = "cr-sandbox:latest"

// Executor runs review checks in a sandboxed environment.
type Executor struct {
	runtime        RuntimeType
	containerImage string
	timeout        time.Duration
	outputLimit    int64
}

// NewExecutor creates a sandbox executor.
func NewExecutor(runtime RuntimeType) *Executor {
	return &Executor{
		runtime:        runtime,
		containerImage: defaultContainerImage,
		timeout:        30 * time.Second,
		outputLimit:    1 << 20, // 1MB
	}
}

// SetTimeout overrides the default execution timeout.
func (e *Executor) SetTimeout(d time.Duration) {
	e.timeout = d
}

// SetContainerImage overrides the default container image.
func (e *Executor) SetContainerImage(image string) {
	e.containerImage = image
}

// RunGoVet executes go vet on the given repo path.
func (e *Executor) RunGoVet(ctx context.Context, repoPath string) *reviewmodel.SandboxRun {
	return e.runCheck(ctx, repoPath, "vet")
}

// RunGoTest executes go test on the given repo path.
func (e *Executor) RunGoTest(ctx context.Context, repoPath string) *reviewmodel.SandboxRun {
	return e.runCheck(ctx, repoPath, "test")
}

func (e *Executor) runCheck(ctx context.Context, repoPath, mode string) *reviewmodel.SandboxRun {
	label := fmt.Sprintf("go %s ./...", mode)
	run := &reviewmodel.SandboxRun{Command: label}

	switch e.runtime {
	case RuntimeFake:
		return e.fakeRun(run)
	case RuntimeContainer:
		return e.containerRun(ctx, repoPath, mode, run)
	case RuntimeLocal:
		return e.localRun(ctx, repoPath, "go", run, mode, "./...")
	default:
		run.Error = fmt.Sprintf("unsupported sandbox runtime: %s", e.runtime)
		return run
	}
}

func (e *Executor) containerRun(
	ctx context.Context, repoPath, mode string, run *reviewmodel.SandboxRun,
) *reviewmodel.SandboxRun {
	if err := checkDocker(); err != nil {
		run.Error = fmt.Sprintf("container runtime unavailable: %v", err)
		return run
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	timeoutSec := int(e.timeout.Seconds())
	if timeoutSec < 1 {
		timeoutSec = 1
	}

	//nolint:gosec // intentional: repoPath mount is the sandbox's core function
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"--network=none",
		"--cap-drop=ALL",
		"--read-only",
		"--tmpfs", "/tmp:exec",
		"-v", repoPath+":/workspace:ro",
		e.containerImage,
		"-mode", mode,
		"-timeout", fmt.Sprintf("%d", timeoutSec),
	)
	cmd.Dir = repoPath

	start := time.Now()
	output, err := cmd.CombinedOutput()
	run.Duration = time.Since(start)
	run.DurationMs = run.Duration.Milliseconds()

	// Parse checkrunner JSON output.
	var cr checkRunnerResult
	if jsonErr := json.Unmarshal(output, &cr); jsonErr == nil {
		run.ExitCode = cr.ExitCode
		run.TimedOut = cr.TimedOut
		run.Stdout = e.truncate(cr.Stdout)
		run.Stderr = e.truncate(cr.Stderr)
		if cr.Error != "" {
			run.Error = cr.Error
		}
		return run
	}

	// Fallback: treat as raw output.
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			run.TimedOut = true
			run.Stderr = fmt.Sprintf("timeout after %s: %s", e.timeout, string(output))
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			run.ExitCode = exitErr.ExitCode()
			run.Stderr = string(output)
		} else {
			run.Error = err.Error()
			run.Stderr = string(output)
		}
	} else {
		run.Stdout = e.truncate(string(output))
	}
	return run
}

func (e *Executor) localRun(
	ctx context.Context, repoPath, command string, run *reviewmodel.SandboxRun, args ...string,
) *reviewmodel.SandboxRun {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = repoPath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		run.Error = fmt.Sprintf("stdout pipe: %v", err)
		return run
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		run.Error = fmt.Sprintf("stderr pipe: %v", err)
		return run
	}
	if err := cmd.Start(); err != nil {
		run.Error = fmt.Sprintf("start: %v", err)
		return run
	}

	start := time.Now()
	stdoutBytes, _ := io.ReadAll(io.LimitReader(stdout, e.outputLimit/2))
	stderrBytes, _ := io.ReadAll(io.LimitReader(stderr, e.outputLimit/2))
	_ = cmd.Wait()
	run.Duration = time.Since(start)
	run.DurationMs = run.Duration.Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		run.TimedOut = true
		run.Stderr = fmt.Sprintf("timeout after %s", e.timeout)
		return run
	}

	run.Stdout = e.truncate(string(stdoutBytes))
	run.Stderr = e.truncate(string(stderrBytes))
	if cmd.ProcessState != nil {
		run.ExitCode = cmd.ProcessState.ExitCode()
	}
	return run
}

type checkRunnerResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	TimedOut   bool   `json:"timed_out"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func (e *Executor) truncate(s string) string {
	if int64(len(s)) > e.outputLimit/2 {
		return s[:e.outputLimit/2] + "\n... [truncated]"
	}
	return s
}

func (e *Executor) fakeRun(run *reviewmodel.SandboxRun) *reviewmodel.SandboxRun {
	run.ExitCode = 0
	run.Stdout = "[fake] sandbox execution skipped"
	run.DurationMs = 0
	return run
}

func checkDocker() error {
	_, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker not found: %w", err)
	}
	return nil
}
