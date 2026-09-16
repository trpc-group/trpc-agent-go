//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package sandboxpython starts one-shot Python guests through the OS sandbox
// runtime. Feature-specific protocols remain in their owning packages.
package sandboxpython

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

const defaultMaxCodeBytes = 64 << 10

// Config controls one sandboxed Python guest.
type Config struct {
	// Python selects the guest interpreter. When empty, the sandbox resolves
	// python3 from its clean PATH.
	Python string
	// Timeout optionally bounds the guest lifetime. The zero value relies on
	// the caller's context without adding a runtime deadline.
	Timeout time.Duration
	// MaxCodeBytes bounds generated code before the process starts. The zero
	// value uses the 64 KiB default; a negative value disables the limit.
	MaxCodeBytes int
}

// Runner owns a sandbox runtime configured for one-shot guest workspaces.
type Runner struct {
	runtime *sandbox.Runtime
}

// New constructs a sandboxed Python runner. Guest workspaces are always
// per-turn and are removed after the process is reaped.
func New(opts ...sandbox.Option) *Runner {
	forced := sandbox.WithSessionPolicy(sandbox.SessionPolicy{
		Persistence:    sandbox.SessionPersistencePerTurn,
		RunConcurrency: sandbox.SessionRunConcurrencyParallel,
	})
	runtimeOpts := append([]sandbox.Option(nil), opts...)
	runtimeOpts = append(runtimeOpts, forced)
	return &Runner{runtime: sandbox.NewRuntime(runtimeOpts...)}
}

// Process is a sandbox process plus its one-shot workspace lifecycle.
type Process struct {
	process *sandbox.Process
	runtime *sandbox.Runtime
	ws      codeexecutor.Workspace

	cleanupOnce sync.Once
	cleanupErr  error
}

// StartScript writes a private bootstrap script and starts it in an empty work
// directory. interpreterArgs precede the script path; scriptArgs follow it.
func (r *Runner) StartScript(
	ctx context.Context,
	cfg Config,
	code string,
	scriptName string,
	script []byte,
	interpreterArgs []string,
	scriptArgs []string,
) (*Process, error) {
	if r == nil || r.runtime == nil {
		return nil, errors.New("sandboxpython: runner is required")
	}
	if err := validateCodeSize(code, cfg.MaxCodeBytes); err != nil {
		return nil, err
	}
	if strings.TrimSpace(scriptName) == "" {
		return nil, errors.New("sandboxpython: script name is required")
	}
	pythonPath, err := resolvePythonPath(cfg.Python)
	if err != nil {
		return nil, err
	}
	ws, err := r.runtime.CreateWorkspace(
		ctx,
		"generated-code/"+uuid.NewString(),
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		return nil, fmt.Errorf("sandboxpython: create workspace: %w", err)
	}
	cleanup := func() {
		_ = r.runtime.Cleanup(context.WithoutCancel(ctx), ws)
	}
	scriptRel := filepath.Join(
		codeexecutor.DirRuns,
		filepath.Base(scriptName),
	)
	if err := r.runtime.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    scriptRel,
		Content: script,
		Mode:    0o600,
	}}); err != nil {
		cleanup()
		return nil, fmt.Errorf("sandboxpython: write guest script: %w", err)
	}
	args := make([]string, 0, len(interpreterArgs)+1+len(scriptArgs))
	args = append(args, interpreterArgs...)
	args = append(args, filepath.Join(ws.Path, scriptRel))
	args = append(args, scriptArgs...)
	process, err := r.runtime.StartProcess(ctx, ws, sandbox.ProcessSpec{
		Cmd:  pythonPath,
		Args: args,
		Env: map[string]string{
			"PYTHONIOENCODING": "utf-8",
			"PYTHONNOUSERSITE": "1",
		},
		CleanEnv: true,
		Cwd:      codeexecutor.DirWork,
		Timeout:  cfg.Timeout,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("sandboxpython: start guest: %w", err)
	}
	return &Process{process: process, runtime: r.runtime, ws: ws}, nil
}

func resolvePythonPath(python string) (string, error) {
	python = strings.TrimSpace(python)
	if python == "" {
		// Let the sandbox resolve its default interpreter from the clean,
		// sandbox-controlled PATH. Resolving the host's preferred Python here
		// could select a path outside the managed profile's read grants.
		return "python3", nil
	}
	pythonPath, err := exec.LookPath(python)
	if err != nil {
		return "", fmt.Errorf("sandboxpython: resolve Python: %w", err)
	}
	if filepath.IsAbs(pythonPath) {
		return pythonPath, nil
	}
	pythonPath, err = filepath.Abs(pythonPath)
	if err != nil {
		return "", fmt.Errorf("sandboxpython: resolve absolute Python path: %w", err)
	}
	return pythonPath, nil
}

func validateCodeSize(code string, maxBytes int) error {
	if maxBytes < 0 {
		return nil
	}
	limit := maxBytes
	if limit == 0 {
		limit = defaultMaxCodeBytes
	}
	if len(code) > limit {
		return fmt.Errorf("sandboxpython: code exceeds %d bytes", limit)
	}
	return nil
}

// Stdin returns the guest standard-input pipe.
func (p *Process) Stdin() io.WriteCloser {
	if p == nil || p.process == nil {
		return nil
	}
	return p.process.Stdin()
}

// Stdout returns the guest standard-output pipe.
func (p *Process) Stdout() io.ReadCloser {
	if p == nil || p.process == nil {
		return nil
	}
	return p.process.Stdout()
}

// Stderr returns the guest standard-error pipe.
func (p *Process) Stderr() io.ReadCloser {
	if p == nil || p.process == nil {
		return nil
	}
	return p.process.Stderr()
}

// Kill terminates the guest process group.
func (p *Process) Kill() error {
	if p == nil || p.process == nil {
		return nil
	}
	return p.process.Kill()
}

// Wait reaps the process and removes its one-shot workspace.
func (p *Process) Wait() error {
	if p == nil || p.process == nil {
		return nil
	}
	waitErr := p.process.Wait()
	p.cleanupOnce.Do(func() {
		p.cleanupErr = p.runtime.Cleanup(context.Background(), p.ws)
	})
	return errors.Join(waitErr, p.cleanupErr)
}
