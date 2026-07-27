//go:build darwin

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
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const macosSandboxExecPath = "/usr/bin/sandbox-exec"

func backendCapabilities(backend BackendType, profile PermissionProfile) backendCapabilitiesInfo {
	supported := backend == BackendAuto || backend == BackendMacOSSandboxExec
	managed := supported && profile.enforcement() == enforcementManaged
	return backendCapabilitiesInfo{
		OSSandbox:                managed,
		PTY:                      false,
		Stdin:                    true,
		NetworkIsolation:         managed,
		DenyReadGlob:             managed,
		Snapshot:                 false,
		Ports:                    false,
		ExternalPathGrants:       managed,
		ProtectedPathMasks:       managed,
		PerCommandGrants:         true,
		RuntimeDenialDiagnostics: managed,
	}
}

func (r *Runtime) osSandboxCommand(
	ctx context.Context,
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
	diagnostics sandboxDenialRun,
) (*exec.Cmd, string, commandCleanup, error) {
	seatbelt, err := r.macosPreflightContext(ctx)
	if err != nil {
		return nil, string(BackendMacOSSandboxExec), nil, err
	}
	policy, err := r.macosSeatbeltProfile(profile, ws, diagnostics)
	if err != nil {
		return nil, string(BackendMacOSSandboxExec), nil, err
	}
	profilePath, err := writeMacOSSeatbeltProfile(policy)
	if err != nil {
		return nil, string(BackendMacOSSandboxExec), nil, backendError(
			ErrSetupFailed,
			string(BackendMacOSSandboxExec),
			err,
		)
	}
	args := []string{"-f", profilePath, "--", spec.Cmd}
	args = append(args, spec.Args...)
	cmd := exec.CommandContext(ctx, seatbelt, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cleanup := func() {
		_ = os.Remove(profilePath)
	}
	return cmd, string(BackendMacOSSandboxExec), cleanup, nil
}

func (r *Runtime) macosPreflight() (string, error) {
	return r.macosPreflightContext(context.Background())
}

func (r *Runtime) macosPreflightContext(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-r.preflightGate:
	}
	defer func() {
		r.preflightGate <- struct{}{}
	}()
	if r.preflightDone {
		return r.seatbeltPath, r.preflightErr
	}
	if r.backend != BackendAuto && r.backend != BackendMacOSSandboxExec {
		r.preflightErr = backendError(
			ErrUnsupportedBackend,
			string(r.backend),
			errors.New("unsupported backend on macOS"),
		)
		r.preflightDone = true
		return "", r.preflightErr
	}
	if _, err := os.Stat(macosSandboxExecPath); err != nil {
		r.preflightErr = backendError(
			ErrSetupFailed,
			string(BackendMacOSSandboxExec),
			errors.New("sandbox-exec executable not found at /usr/bin/sandbox-exec"),
		)
		r.preflightDone = true
		return "", r.preflightErr
	}
	stderr, err := runMacOSSeatbeltPreflightProbe(ctx, macosSandboxExecPath)
	return r.completeMacOSPreflight(ctx, stderr, err)
}

func (r *Runtime) completeMacOSPreflight(
	ctx context.Context,
	stderr string,
	err error,
) (string, error) {
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		r.preflightErr = backendError(
			ErrSetupFailed,
			string(BackendMacOSSandboxExec),
			seatbeltProbeError{err: err, stderr: stderr},
		)
		r.preflightDone = true
		return "", r.preflightErr
	}
	r.seatbeltPath = macosSandboxExecPath
	r.preflightDone = true
	return r.seatbeltPath, nil
}

func runMacOSSeatbeltPreflightProbe(parent context.Context, seatbelt string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	profilePath, err := writeMacOSSeatbeltProfile(macosPreflightPolicy())
	if err != nil {
		return "", err
	}
	defer os.Remove(profilePath)
	var stderr bytes.Buffer
	probe := exec.CommandContext(ctx, seatbelt, "-f", profilePath, "--", "/usr/bin/true")
	probe.Stderr = &stderr
	err = probe.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stderr.String(), err
}

func writeMacOSSeatbeltProfile(policy string) (string, error) {
	f, err := os.CreateTemp("", "trpc-agent-go-seatbelt-*.sb")
	if err != nil {
		return "", err
	}
	path := f.Name()
	_, writeErr := f.WriteString(policy)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	return filepath.Clean(path), nil
}

type seatbeltProbeError struct {
	err    error
	stderr string
}

func (e seatbeltProbeError) Error() string {
	if e.stderr == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.stderr
}

func (e seatbeltProbeError) Unwrap() error {
	return e.err
}
