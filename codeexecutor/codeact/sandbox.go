//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package codeact

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/internal/coderuntime/sandboxpython"
)

const sandboxGuestStderrLimit = 1 << 20

// SandboxRunner executes tool-orchestration Python through
// codeexecutor/sandbox. Its default options use a fresh OS-sandboxed workspace.
// Use NewSandboxRunner to construct it. Configure exported fields before first
// use; mutating them concurrently with ExecuteCodeAct is not safe.
type SandboxRunner struct {
	// Python selects an interpreter through the host PATH and may require
	// sandbox read grants. When empty, the sandbox resolves python3 from its
	// clean PATH.
	Python string
	// Timeout sets an optional deadline for the full execution and propagates
	// cancellation to host tool calls. Tool handlers must honor their context
	// for timely cancellation. The zero value relies on the caller's context.
	Timeout time.Duration
	// MaxCodeBytes bounds generated code before the process starts. The default
	// is 64 KiB. Use a negative value to disable the limit.
	MaxCodeBytes int

	runner *sandboxpython.Runner
}

// NewSandboxRunner creates a runner backed by codeexecutor/sandbox. The default
// options require managed OS sandboxing. Guest workspaces are always one-shot
// even if opts contain another session policy.
func NewSandboxRunner(opts ...sandbox.Option) *SandboxRunner {
	return &SandboxRunner{runner: sandboxpython.New(opts...)}
}

// ExecuteCodeAct implements Runtime using a fresh sandboxed Python guest.
func (r *SandboxRunner) ExecuteCodeAct(
	ctx context.Context,
	req Request,
	handler ToolCallHandler,
) (Result, error) {
	if r == nil || r.runner == nil {
		return Result{}, errRequired("sandbox runner")
	}
	runCtx, cancel := localExecutionContext(ctx, r.Timeout)
	defer cancel()
	return executeStdio(runCtx, r, req, handler)
}

func (r *SandboxRunner) start(
	ctx context.Context,
	req Request,
	script string,
) (stdioProcess, error) {
	process, err := r.runner.StartScript(
		ctx,
		sandboxpython.Config{
			Python:       r.Python,
			MaxCodeBytes: r.MaxCodeBytes,
		},
		req.Code,
		"guest.py",
		[]byte(script),
		[]string{"-u"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("sandbox guest: %w", err)
	}
	stderr := newSandboxStderrCapture(sandboxGuestStderrLimit)
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderr, process.Stderr())
		close(stderrDone)
	}()
	return &sandboxStdioProcess{
		process:    process,
		stderr:     stderr,
		stderrDone: stderrDone,
	}, nil
}

type sandboxStdioProcess struct {
	process    *sandboxpython.Process
	stderr     *sandboxStderrCapture
	stderrDone <-chan struct{}
}

func (p *sandboxStdioProcess) Stdin() io.WriteCloser { return p.process.Stdin() }

func (p *sandboxStdioProcess) Stdout() io.ReadCloser { return p.process.Stdout() }

func (p *sandboxStdioProcess) Kill() error { return p.process.Kill() }

func (p *sandboxStdioProcess) diagnosticOutput() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	output := p.stderr.String()
	if p.stderr.Exceeded() {
		output += "\n[stderr truncated]"
	}
	return output
}

func (p *sandboxStdioProcess) Wait() error {
	if p.stderrDone != nil {
		<-p.stderrDone
	}
	return p.process.Wait()
}

type sandboxStderrCapture struct {
	mu       sync.Mutex
	limit    int
	buf      bytes.Buffer
	exceeded bool
}

func newSandboxStderrCapture(limit int) *sandboxStderrCapture {
	return &sandboxStderrCapture{limit: limit}
}

func (b *sandboxStderrCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.exceeded = true
	}
	return len(p), nil
}

func (b *sandboxStderrCapture) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *sandboxStderrCapture) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

var _ Runtime = (*SandboxRunner)(nil)
