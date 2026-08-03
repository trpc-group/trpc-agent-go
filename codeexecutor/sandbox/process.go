//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// ProcessSpec describes a full-duplex sandbox process. Input is written through
// Process.Stdin after startup rather than supplied in the spec.
type ProcessSpec struct {
	// Cmd is the program to execute.
	Cmd string
	// Args are passed to Cmd.
	Args []string
	// Env adds or overrides process environment variables.
	Env map[string]string
	// CleanEnv prevents host environment inheritance. Sandbox policy variables,
	// Env, and sandbox-owned workspace variables are still applied.
	CleanEnv bool
	// Cwd is the working directory relative to the workspace root. The default
	// is codeexecutor.DirWork.
	Cwd string
	// Timeout optionally bounds the process lifetime. The zero value relies on
	// the caller's context without adding a runtime deadline.
	Timeout time.Duration
	// Limits requests resource limits supported by the active backend.
	Limits codeexecutor.ResourceLimits
}

func (s ProcessSpec) runProgramSpec() codeexecutor.RunProgramSpec {
	return codeexecutor.RunProgramSpec{
		Cmd:     s.Cmd,
		Args:    s.Args,
		Env:     s.Env,
		Cwd:     s.Cwd,
		Timeout: s.Timeout,
		Limits:  s.Limits,
	}
}

// Process is a running sandboxed program with separate standard streams.
// Callers must call Wait after the process exits or is killed. Abandoning a
// Process without Wait can retain backend resources and, under serial workspace
// concurrency, permanently block later runs on the same workspace.
type Process struct {
	prepared    *preparedProcess
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	stderrSetup *processStderrSetupTracker

	waitOnce sync.Once
	waitDone chan struct{}
	waitErr  error
}

type preparedProcess struct {
	cmd     *exec.Cmd
	backend string
	profile PermissionProfile
	runCtx  context.Context
	release func()
	once    sync.Once
}

func (p *preparedProcess) cleanup() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.release != nil {
			p.release()
		}
	})
}

// StartProcess starts a sandboxed program and returns its standard streams.
// A zero spec.Timeout adds no runtime deadline and relies on ctx. The caller
// owns the protocol, output limits, and stream draining. Every successfully
// returned Process must eventually be reaped with Wait, including after Kill or
// context cancellation.
func (r *Runtime) StartProcess(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec ProcessSpec,
) (*Process, error) {
	prepared, err := r.prepareProcess(ctx, ws, spec)
	if err != nil {
		return nil, err
	}
	stdin, err := prepared.cmd.StdinPipe()
	if err != nil {
		prepared.cleanup()
		return nil, fmt.Errorf("sandbox: create process stdin: %w", err)
	}
	stdout, err := prepared.cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		prepared.cleanup()
		return nil, fmt.Errorf("sandbox: create process stdout: %w", err)
	}
	stderr, err := prepared.cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		prepared.cleanup()
		return nil, fmt.Errorf("sandbox: create process stderr: %w", err)
	}
	var stderrSetup *processStderrSetupTracker
	if prepared.profile.network.Mode == NetworkControlled {
		stderrSetup, err = newProcessStderrSetupTracker(stderr)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			prepared.cleanup()
			return nil, fmt.Errorf("sandbox: track process stderr: %w", err)
		}
	}
	if err := prepared.cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if stderrSetup != nil {
			_ = stderrSetup.reader.Close()
			_ = stderrSetup.writer.Close()
		}
		prepared.cleanup()
		return nil, backendError(ErrSetupFailed, prepared.backend, err)
	}
	if stderrSetup != nil {
		stderrSetup.start()
		stderr = stderrSetup.reader
	}
	return &Process{
		prepared:    prepared,
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		stderrSetup: stderrSetup,
		waitDone:    make(chan struct{}),
	}, nil
}

func (r *Runtime) prepareProcess(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec ProcessSpec,
) (*preparedProcess, error) {
	runSpec := spec.runProgramSpec()
	prep, err := r.prepareRun(ctx, ws, runSpec)
	if err != nil {
		return nil, err
	}
	unlock := func() {}
	if r.sessionPolicy.RunConcurrency == SessionRunConcurrencySerial {
		unlock, err = r.lockWorkspaceRunContext(ctx, ws)
		if err != nil {
			return nil, err
		}
	}
	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	env := r.buildProcessEnvironment(ws, spec)
	cmd, backendName, backendCleanup, err := r.commandForProfile(
		runCtx, prep.profile, ws, prep.cwd, env, runSpec, sandboxDenialRun{},
	)
	if err != nil {
		cancel()
		unlock()
		return nil, err
	}
	setupProcess(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second
	return &preparedProcess{
		cmd:     cmd,
		backend: backendName,
		profile: prep.profile,
		runCtx:  runCtx,
		release: func() {
			if backendCleanup != nil {
				backendCleanup()
			}
			cancel()
			unlock()
		},
	}, nil
}

// Stdin returns the process standard-input pipe.
func (p *Process) Stdin() io.WriteCloser {
	if p == nil {
		return nil
	}
	return p.stdin
}

// Stdout returns the process standard-output pipe.
func (p *Process) Stdout() io.ReadCloser {
	if p == nil {
		return nil
	}
	return p.stdout
}

// Stderr returns the process standard-error pipe.
func (p *Process) Stderr() io.ReadCloser {
	if p == nil {
		return nil
	}
	return p.stderr
}

// Wait waits for process exit and releases backend resources exactly once. On
// Unix-like systems it also makes a best-effort attempt to terminate remaining
// members of the process group after the leader exits. Concurrent and repeated
// calls return the same result.
func (p *Process) Wait() error {
	if p == nil || p.prepared == nil || p.prepared.cmd == nil {
		return nil
	}
	p.waitOnce.Do(func() {
		p.waitErr = p.prepared.cmd.Wait()
		cleanupProcessTree(p.prepared.cmd)
		if p.waitErr != nil {
			processErr := p.waitErr
			switch p.prepared.runCtx.Err() {
			case context.DeadlineExceeded:
				p.waitErr = &sandboxError{
					Kind:    ErrTimeout,
					Op:      "process",
					Backend: p.prepared.backend,
					Err: errors.Join(
						context.DeadlineExceeded,
						processErr,
					),
				}
			case context.Canceled:
				p.waitErr = errors.Join(context.Canceled, processErr)
			default:
				exitCode, exitErr := exitCodeFromWait(processErr, false)
				if exitErr == nil {
					if setupErr := mapControlledEgressSetupExit(
						p.prepared.profile,
						exitCode,
						p.stderrSetup.setupMarkerSeen(),
						p.stderrSetup.userExitMarkerSeen(),
					); setupErr != nil {
						p.waitErr = setupErr
					}
				}
			}
		}
		p.prepared.cleanup()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

// Kill requests termination of the sandboxed process. On Unix-like systems it
// targets the process group; other platforms use their available process
// termination mechanism. Call Wait afterward to release all backend resources.
// Killing an already-exited process succeeds.
func (p *Process) Kill() error {
	if p == nil || p.prepared == nil {
		return nil
	}
	err := killProcessGroup(p.prepared.cmd)
	if errors.Is(err, os.ErrProcessDone) || isProcessDoneError(err) {
		return nil
	}
	return err
}
