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
	"fmt"
	"os/exec"
	"strings"
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

// Executor runs review checks in a sandboxed environment.
type Executor struct {
	runtime     RuntimeType
	timeout     time.Duration
	outputLimit int64
}

// NewExecutor creates a sandbox executor.
// runtime "local" is only allowed when explicitly specified via the type parameter.
func NewExecutor(runtime RuntimeType) *Executor {
	return &Executor{
		runtime:     runtime,
		timeout:     30 * time.Second,
		outputLimit: 1 << 20, // 1MB
	}
}

// SetTimeout overrides the default execution timeout.
func (e *Executor) SetTimeout(d time.Duration) {
	e.timeout = d
}

// RunGoVet executes go vet on the given repo path.
func (e *Executor) RunGoVet(ctx context.Context, repoPath string) *reviewmodel.SandboxRun {
	return e.runCommand(ctx, repoPath, "go", "vet", "./...")
}

// RunGoTest executes go test on the given repo path.
func (e *Executor) RunGoTest(ctx context.Context, repoPath string) *reviewmodel.SandboxRun {
	return e.runCommand(ctx, repoPath, "go", "test", "-count=1", "-timeout=20s", "./...")
}

func (e *Executor) runCommand(ctx context.Context, repoPath string, command string, args ...string) *reviewmodel.SandboxRun {
	run := &reviewmodel.SandboxRun{
		Command: fmt.Sprintf("%s %s", command, strings.Join(args, " ")),
	}

	if e.runtime == RuntimeFake {
		return e.fakeRun(run)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = repoPath

	start := time.Now()
	output, err := cmd.CombinedOutput()
	run.Duration = time.Since(start)
	run.DurationMs = run.Duration.Milliseconds()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			run.TimedOut = true
			run.Stderr = fmt.Sprintf("timeout after %s: %s", e.timeout, string(output))
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			run.ExitCode = exitErr.ExitCode()
			run.Stderr = string(output)
			run.Stdout = ""
		} else {
			run.Error = err.Error()
			run.Stderr = string(output)
		}
	} else {
		run.ExitCode = 0
		run.Stdout = string(output)
	}

	// Truncate output to limit.
	total := int64(len(run.Stdout)) + int64(len(run.Stderr))
	if total > e.outputLimit {
		if len(run.Stdout) > int(e.outputLimit/2) {
			run.Stdout = run.Stdout[:e.outputLimit/2] + "\n... [truncated]"
		}
		if len(run.Stderr) > int(e.outputLimit/2) {
			run.Stderr = run.Stderr[:e.outputLimit/2] + "\n... [truncated]"
		}
	}

	return run
}

func (e *Executor) fakeRun(run *reviewmodel.SandboxRun) *reviewmodel.SandboxRun {
	run.ExitCode = 0
	run.Stdout = "[fake] sandbox execution skipped"
	run.DurationMs = 0
	return run
}
