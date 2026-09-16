//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package dynamicworkflow

import (
	"context"
	"fmt"
	"io"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/internal/coderuntime/sandboxpython"
)

// SandboxRunner executes workflow Python through codeexecutor/sandbox. Its
// default options use a fresh OS-sandboxed workspace. Use NewSandboxRunner to
// construct it. Configure exported fields before first use; mutating them
// concurrently with ExecuteWorkflow is not safe.
type SandboxRunner struct {
	// Python selects an interpreter through the host PATH and may require
	// sandbox read grants. When empty, the sandbox resolves python3 from its
	// clean PATH.
	Python string
	// Timeout sets an optional deadline for the full execution and propagates
	// cancellation to host callbacks. Call handlers must honor their context
	// for timely cancellation. The zero value relies on the caller's context.
	Timeout time.Duration
	// MaxCodeBytes bounds workflow source before the process starts. The
	// default is 64 KiB. Use a negative value to disable the limit.
	MaxCodeBytes int

	runner *sandboxpython.Runner
}

// NewSandboxRunner creates a runner backed by codeexecutor/sandbox. The default
// options require managed OS sandboxing. Guest workspaces are always one-shot
// even if opts contain another session policy.
func NewSandboxRunner(opts ...sandbox.Option) *SandboxRunner {
	return &SandboxRunner{runner: sandboxpython.New(opts...)}
}

// ExecuteWorkflow implements Runtime with a fresh sandboxed Python guest.
func (r *SandboxRunner) ExecuteWorkflow(
	ctx context.Context,
	req Request,
	handler CallHandler,
) (Result, error) {
	if r == nil || r.runner == nil {
		return Result{}, required("sandbox runner")
	}
	if handler == nil {
		return Result{}, required("call handler")
	}
	runCtx, cancel := localExecutionContext(ctx, r.Timeout)
	defer cancel()
	guest, err := r.startSandboxGuest(runCtx, req.Code)
	if err != nil {
		return Result{}, err
	}
	return runWorkflowGuest(runCtx, guest, handler)
}

func (r *SandboxRunner) startSandboxGuest(
	ctx context.Context,
	code string,
) (*workflowGuestProcess, error) {
	process, err := r.runner.StartScript(
		ctx,
		sandboxpython.Config{
			Python:       r.Python,
			MaxCodeBytes: r.MaxCodeBytes,
		},
		code,
		"guest.py",
		[]byte(pythonGuest),
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: start sandbox guest: %w", err)
	}
	stderr := newLimitedBuffer(workflowGuestCapturedOutputLimit)
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderr, process.Stderr())
		close(stderrDone)
	}()
	return &workflowGuestProcess{
		process:    process,
		stdin:      process.Stdin(),
		stdout:     process.Stdout(),
		stderr:     stderr,
		stderrDone: stderrDone,
		code:       code,
	}, nil
}

var _ Runtime = (*SandboxRunner)(nil)
