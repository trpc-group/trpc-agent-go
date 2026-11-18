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
	"io"
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
	result, err := r.runProgram(ctx, ws, spec)
	return normalizeRunProgramResult(result, err)
}

func normalizeRunProgramResult(
	result codeexecutor.RunResult,
	err error,
) (codeexecutor.RunResult, error) {
	if err == nil {
		return result, nil
	}
	var classified *sandboxError
	if errors.As(err, &classified) {
		return result, err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return result, err
	}
	result.TimedOut = true
	result.ExitCode = -1
	return result, newSandboxError(ErrTimeout, "run", "", err)
}

func (r *Runtime) runProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	diagnosticsCh := diagnosticsChanFromContext(ctx)
	runDiagnostics := Diagnostics{}
	if diagnosticsCh != nil {
		defer func() {
			select {
			case diagnosticsCh <- runDiagnostics:
			default:
			}
		}()
	}
	prep, err := r.prepareRun(ctx, ws, spec)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	if r.sessionPolicy.RunConcurrency == SessionRunConcurrencySerial {
		unlock, err := r.lockWorkspaceRunContext(ctx, ws)
		if err != nil {
			return codeexecutor.RunResult{}, err
		}
		defer unlock()
	}
	runCtx, cancel := context.WithTimeout(ctx, prep.timeout)
	defer cancel()
	start := time.Now()
	env := r.buildEnvironment(ws, spec)
	diagnostics := sandboxDenialRun{}
	if diagnosticsCh != nil && prep.profile.enforcement() == enforcementManaged {
		_ = r.ensureDenialMonitor(runCtx)
		if result, err, done := runContextResult(runCtx, start); done {
			return result, err
		}
		if r.sandboxDenialCollectingReady() {
			diagnostics = r.sandboxDenialRunForCollecting(prep.profile)
		}
	}
	cmd, backendName, cleanup, err := r.commandForProfile(
		runCtx, prep.profile, ws, prep.cwd, env, spec, diagnostics,
	)
	if err != nil {
		if result, ctxErr, done := runContextResult(runCtx, start); done {
			return result, ctxErr
		}
		return codeexecutor.RunResult{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	stdout := newLimitedBuffer(r.outputMaxBytes)
	stderr := newLimitedBuffer(r.outputMaxBytes)
	cmd.Stdout = stdout
	var stderrMarkers *controlledEgressMarkerTracker
	if prep.profile.network.Mode == NetworkControlled {
		stderrMarkers = newControlledEgressMarkerTracker(
			controlledEgressSetupToken(cmd.Args),
		)
		cmd.Stderr = io.MultiWriter(stderr, stderrMarkers)
	} else {
		cmd.Stderr = stderr
	}
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
		if result, ctxErr, done := runContextResult(runCtx, start); done {
			return result, ctxErr
		}
		return codeexecutor.RunResult{}, backendError(ErrSetupFailed, backendName, err)
	}
	// Parent ExtraFiles are no longer needed once the child has inherited them.
	releaseCmdExtraFiles(cmd)
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
	runDiagnostics = r.collectRunDiagnostics(runCtx, diagnostics, spec.Cmd, timedOut)
	if timedOut {
		return result, &sandboxError{
			Kind:    ErrTimeout,
			Op:      "run",
			Backend: backendName,
			Err:     context.DeadlineExceeded,
		}
	}
	if err := mapControlledEgressSetupExit(
		prep.profile,
		exitCode,
		stderrMarkers.setupMarkerSeen(),
	); err != nil {
		return result, err
	}
	return result, nil
}

// collectRunDiagnostics collects denial diagnostics for a finished run. A run
// that hit its deadline leaves runCtx canceled, so collection then uses its own
// bounded context to keep settle waits effective.
func (r *Runtime) collectRunDiagnostics(
	runCtx context.Context,
	run sandboxDenialRun,
	cmd string,
	timedOut bool,
) Diagnostics {
	if !run.enabled {
		return Diagnostics{}
	}
	collectCtx := runCtx
	if timedOut {
		var cancel context.CancelFunc
		collectCtx, cancel = context.WithTimeout(
			context.Background(),
			sandboxDenialSettleTimeout,
		)
		defer cancel()
	}
	denials, truncated := r.collectSandboxDenials(
		collectCtx,
		run,
		cmd,
		sandboxDenialSettleTimeout,
	)
	return Diagnostics{Denials: denials, Truncated: truncated}
}

func runContextResult(
	ctx context.Context,
	start time.Time,
) (codeexecutor.RunResult, error, bool) {
	err := ctx.Err()
	if err == nil {
		return codeexecutor.RunResult{}, nil, false
	}
	result := codeexecutor.RunResult{
		Duration: time.Since(start),
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.TimedOut = true
		result.ExitCode = -1
		return result, &sandboxError{
			Kind: ErrTimeout,
			Op:   "run",
			Err:  context.DeadlineExceeded,
		}, true
	}
	return result, err, true
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
	if err := validateProfileNetworkPolicy(profile); err != nil {
		return runPreparation{}, err
	}
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
	diagnostics sandboxDenialRun,
) (*exec.Cmd, string, commandCleanup, error) {
	if err := validateProfileNetworkPolicy(profile); err != nil {
		return nil, "", nil, err
	}
	switch profile.enforcement() {
	case enforcementDisabled:
		// #nosec G204 -- RunProgram intentionally executes caller-provided
		// commands when sandboxing is explicitly disabled.
		cmd := exec.CommandContext(ctx, spec.Cmd, spec.Args...)
		cmd.Dir = cwd
		cmd.Env = env
		return cmd, "disabled", nil, nil
	case enforcementExternal:
		return nil, "external", nil, backendError(
			ErrUnsupportedBackend,
			"external",
			errors.New("external sandbox profile cannot be executed by local sandbox runtime"),
		)
	default:
		return r.osSandboxCommand(ctx, profile, ws, cwd, env, spec, diagnostics)
	}
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
