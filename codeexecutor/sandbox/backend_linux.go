//go:build linux

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
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func backendCapabilities(backend BackendType, profile PermissionProfile) BackendCapabilities {
	_ = backend
	enforcement := profile.Enforcement()
	managed := enforcement == EnforcementManaged
	return BackendCapabilities{
		OSSandbox:          managed && runtime.GOOS == "linux",
		PTY:                false,
		Stdin:              true,
		NetworkIsolation:   managed,
		DenyReadGlob:       managed,
		Snapshot:           false,
		Ports:              false,
		ExternalPathGrants: managed,
		ProtectedPathMasks: managed,
		PerCommandGrants:   true,
	}
}

func (r *Runtime) osSandboxCommand(
	ctx context.Context,
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
) (*exec.Cmd, string, error) {
	_ = ctx
	bwrap, mountProc, err := r.linuxPreflight()
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), err
	}
	if err := r.prepareProtectedMasks(profile, ws); err != nil {
		return nil, string(BackendLinuxBubblewrap), err
	}
	args, err := r.linuxSandboxArgs(profile, ws, cwd, env, spec, mountProc)
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), err
	}
	cmd := exec.CommandContext(ctx, bwrap, args...)
	return cmd, string(BackendLinuxBubblewrap), nil
}

func (r *Runtime) linuxSandboxArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
	mountProc bool,
) ([]string, error) {
	args := []string{
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
		"--new-session",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
	}
	if mountProc {
		args = append(args, "--proc", "/proc")
	}
	if profile.Network.Mode == NetworkRestricted {
		args = append(args, "--unshare-net")
	}
	grantArgs, err := r.externalGrantArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	args = append(args, grantArgs...)
	args = append(args, "--bind", ws.Path, ws.Path)
	protectedArgs, err := r.protectedMaskArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	args = append(args, protectedArgs...)
	denyArgs, err := r.denyReadMaskArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	args = append(args, denyArgs...)
	args = append(args, "--clearenv")
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		args = append(args, "--setenv", k, v)
	}
	args = append(args, "--chdir", cwd, "--", spec.Cmd)
	args = append(args, spec.Args...)
	return args, nil
}

func (r *Runtime) linuxPreflight() (string, bool, error) {
	r.preflightOnce.Do(func() {
		if r.backend != BackendAuto && r.backend != BackendLinuxBubblewrap {
			r.preflightErr = backendError(
				ErrUnsupportedBackend,
				string(r.backend),
				errors.New("unsupported backend on linux"),
			)
			return
		}
		if isWSL1() {
			r.preflightErr = backendError(
				ErrUnsupportedBackend,
				string(BackendLinuxBubblewrap),
				errors.New("WSL1 does not provide the required namespace support"),
			)
			return
		}
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			r.preflightErr = backendError(
				ErrSetupFailed,
				string(BackendLinuxBubblewrap),
				errors.New("bubblewrap executable not found in PATH"),
			)
			return
		}
		stderr, err := runBwrapPreflightProbe(bwrap, true)
		if err == nil {
			r.bwrapPath = bwrap
			r.bwrapMountProc = true
			return
		}
		if isProcMountFailure(stderr) {
			stderr, err = runBwrapPreflightProbe(bwrap, false)
			if err == nil {
				r.bwrapPath = bwrap
				r.bwrapMountProc = false
				return
			}
		}
		if err != nil {
			r.preflightErr = backendError(
				ErrSetupFailed,
				string(BackendLinuxBubblewrap),
				bwrapProbeError{err: err, stderr: stderr},
			)
			return
		}
	})
	return r.bwrapPath, r.bwrapMountProc, r.preflightErr
}

// runBwrapPreflightProbe runs a short-lived bubblewrap probe and captures stderr.
//
// Strategy:
//   - linuxPreflight first runs /bin/true under bubblewrap with --proc /proc.
//   - The goal is to detect environments where mounting a fresh /proc fails, for
//     example restricted Docker-style containers, so the real run can retry
//     without --proc while keeping PID isolation.
//   - stderr is captured instead of streamed because this is a one-shot probe with
//     a trivial command and a short timeout.
func runBwrapPreflightProbe(bwrap string, mountProc bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{
		"--die-with-parent",
		"--unshare-user",
		"--ro-bind", "/", "/",
	}
	if mountProc {
		args = append(args, "--proc", "/proc")
	}
	args = append(args, "--", "/bin/true")
	var stderr bytes.Buffer
	probe := exec.CommandContext(ctx, bwrap, args...)
	probe.Stderr = &stderr
	err := probe.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stderr.String(), err
}

type bwrapProbeError struct {
	err    error
	stderr string
	hint   string
}

func (e bwrapProbeError) Error() string {
	stderr := strings.TrimSpace(e.stderr)
	msg := e.err.Error()
	if stderr != "" {
		msg += ": " + stderr
	}
	if e.hint != "" {
		msg += "; " + e.hint
	}
	return msg
}

func (e bwrapProbeError) Unwrap() error {
	return e.err
}

func isProcMountFailure(stderr string) bool {
	return strings.Contains(stderr, "Can't mount proc") &&
		strings.Contains(stderr, "/newroot/proc") &&
		containsAny(stderr, []string{
			"Invalid argument",
			"Operation not permitted",
			"Permission denied",
		})
}

func containsAny(s string, substrings []string) bool {
	for _, substring := range substrings {
		if strings.Contains(s, substring) {
			return true
		}
	}
	return false
}

func isWSL1() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	version := strings.ToLower(string(data))
	return strings.Contains(version, "microsoft") &&
		!strings.Contains(version, "microsoft-standard") &&
		!strings.Contains(version, "wsl2")
}

func (r *Runtime) prepareProtectedMasks(profile PermissionProfile, ws codeexecutor.Workspace) error {
	meta := filepath.Join(ws.Path, ".trpc-agent-sandbox")
	if err := os.MkdirAll(meta, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(meta, 0o700); err != nil {
		return err
	}
	mask := denyReadMaskSource(ws)
	_ = os.Chmod(mask, 0o600)
	if err := os.WriteFile(mask, nil, 0o000); err != nil {
		return err
	}
	if err := os.Chmod(mask, 0o000); err != nil {
		return err
	}
	for _, rel := range profile.FileSystem.ProtectedMetadata {
		rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel == "" || rel == "." {
			continue
		}
		if strings.HasPrefix(rel, "../") {
			return deniedf(ErrPathDenied, "protect", rel, "protected path escapes workspace")
		}
		abs := filepath.Join(ws.Path, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			if err := os.MkdirAll(abs, 0o555); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) protectedMaskArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	var args []string
	for _, rel := range profile.FileSystem.ProtectedMetadata {
		rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel == "" || rel == "." {
			continue
		}
		abs := filepath.Join(ws.Path, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		args = append(args, "--ro-bind", abs, abs)
	}
	return args, nil
}

func (r *Runtime) denyReadMaskArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	matches, err := r.deniedReadMatches(profile, ws)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			args = append(args, "--tmpfs", match)
			continue
		}
		args = append(args, "--ro-bind", denyReadMaskSource(ws), match)
	}
	return args, nil
}

func denyReadMaskSource(ws codeexecutor.Workspace) string {
	return filepath.Join(ws.Path, ".trpc-agent-sandbox", "deny-read-mask")
}

func (r *Runtime) externalGrantArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, rule := range profile.FileSystem.Rules {
		if rule.Kind != RulePath || rule.Path == "" || !filepath.IsAbs(rule.Path) {
			continue
		}
		target, err := filepath.Abs(rule.Path)
		if err != nil {
			return nil, err
		}
		if sameOrChild(wsAbs, target) {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			return nil, deniedf(ErrPathDenied, "grant", target, "external grant target unavailable")
		}
		switch rule.Access {
		case AccessRead:
			args = append(args, "--ro-bind", target, target)
		case AccessWrite:
			args = append(args, "--bind", target, target)
		}
	}
	return args, nil
}
