//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// RunProgram executes a command in the workspace under the active sandbox
// policy.
func (r *Runtime) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	prep, err := r.prepareRun(ctx, ws, spec)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, prep.timeout)
	defer cancel()
	if r.sessionPolicy.RunConcurrency == SessionRunConcurrencySerial {
		lock := r.runLock(ws)
		lock.Lock()
		defer lock.Unlock()
	}
	start := time.Now()
	env := r.buildEnvironment(ws, spec)
	cmd, backendName, err := r.commandForProfile(runCtx, prep.profile, ws, prep.cwd, env, spec)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	stdout := newLimitedBuffer(r.outputMaxBytes)
	stderr := newLimitedBuffer(r.outputMaxBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	} else {
		cmd.Stdin = nil
	}
	setupProcess(cmd)
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Start()
	if err != nil {
		return codeexecutor.RunResult{}, backendError(ErrSetupFailed, backendName, err)
	}
	waitErr := cmd.Wait()
	duration := time.Since(start)
	timedOut := runCtx.Err() == context.DeadlineExceeded
	if timedOut {
		killProcessGroup(cmd)
	}
	exitCode, err := exitCodeFromWait(waitErr, timedOut)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	result := codeexecutor.RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
		TimedOut: timedOut,
	}
	if timedOut {
		return result, &sandboxError{
			Kind:    ErrTimeout,
			Op:      "run",
			Backend: backendName,
			Err:     context.DeadlineExceeded,
		}
	}
	return result, nil
}

type runPreparation struct {
	profile PermissionProfile
	cwd     string
	timeout time.Duration
}

func (r *Runtime) prepareRun(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (runPreparation, error) {
	if spec.Cmd == "" {
		return runPreparation{}, deniedf(
			ErrPolicyViolation, "run", "", "empty command",
		)
	}
	profile := applyAdditionalPermissions(
		normalizeProfile(r.profile),
		additionalPermissionsFromContext(ctx),
	)
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return runPreparation{}, err
	}
	for _, dir := range []string{"home", "tmp"} {
		if err := ensureDir(filepath.Join(ws.Path, dir)); err != nil {
			return runPreparation{}, err
		}
	}
	cwdRel := spec.Cwd
	if cwdRel == "" {
		cwdRel = codeexecutor.DirWork
	}
	if err := r.checkRead(profile, ws, cwdRel); err != nil {
		return runPreparation{}, err
	}
	cwd, _, err := r.resolveWorkspacePath(ws, cwdRel)
	if err != nil {
		return runPreparation{}, err
	}
	if err := r.ensureRunCwd(profile, ws, cwdRel, cwd); err != nil {
		return runPreparation{}, err
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	return runPreparation{profile: profile, cwd: cwd, timeout: timeout}, nil
}

func (r *Runtime) ensureRunCwd(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwdRel string,
	cwd string,
) error {
	if _, err := os.Stat(cwd); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := r.checkWrite(profile, ws, cwdRel); err != nil {
			return err
		}
	}
	return ensureDir(cwd)
}

func exitCodeFromWait(waitErr error, timedOut bool) (int, error) {
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if timedOut {
		return -1, nil
	}
	return 0, waitErr
}

func (r *Runtime) commandForProfile(
	ctx context.Context,
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
) (*exec.Cmd, string, error) {
	switch profile.enforcement() {
	case enforcementDisabled:
		// #nosec G204 -- RunProgram intentionally executes caller-provided
		// commands when sandboxing is explicitly disabled.
		cmd := exec.CommandContext(ctx, spec.Cmd, spec.Args...)
		cmd.Dir = cwd
		cmd.Env = env
		return cmd, "disabled", nil
	case enforcementExternal:
		return nil, "external", backendError(
			ErrUnsupportedBackend,
			"external",
			errors.New("external sandbox profile cannot be executed by local sandbox runtime"),
		)
	default:
		return r.osSandboxCommand(ctx, profile, ws, cwd, env, spec)
	}
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
