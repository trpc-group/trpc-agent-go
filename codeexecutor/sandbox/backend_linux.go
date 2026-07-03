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

func backendCapabilities(backend BackendType, profile PermissionProfile) backendCapabilitiesInfo {
	_ = backend
	enforcement := profile.enforcement()
	managed := enforcement == enforcementManaged
	return backendCapabilitiesInfo{
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
	diagnostics sandboxDenialRun,
) (*exec.Cmd, string, commandCleanup, error) {
	_ = ctx
	_ = diagnostics
	bwrap, mountProc, err := r.linuxPreflight()
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	if err := r.prepareProtectedMasks(profile, ws); err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	setup, err := r.linuxSandboxSetup(profile, ws, cwd, env, spec, mountProc)
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	cmd := exec.CommandContext(ctx, bwrap, setup.args...)
	var cleanup commandCleanup
	if setup.needsDenyReadDataFD {
		nullFile, err := os.Open("/dev/null")
		if err != nil {
			return nil, string(BackendLinuxBubblewrap), nil, err
		}
		cmd.ExtraFiles = []*os.File{nullFile}
		cleanup = func() {
			_ = nullFile.Close()
			cleanupSyntheticDenyReadMaskTargets(setup.syntheticDenyReadTargets)
		}
	} else if len(setup.syntheticDenyReadTargets) != 0 {
		cleanup = func() {
			cleanupSyntheticDenyReadMaskTargets(setup.syntheticDenyReadTargets)
		}
	}
	return cmd, string(BackendLinuxBubblewrap), cleanup, nil
}

type linuxSandboxSetup struct {
	args                     []string
	syntheticDenyReadTargets []string
	needsDenyReadDataFD      bool
}

func (r *Runtime) linuxSandboxArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
	mountProc bool,
) ([]string, error) {
	setup, err := r.linuxSandboxSetup(profile, ws, cwd, env, spec, mountProc)
	if err != nil {
		return nil, err
	}
	return setup.args, nil
}

func (r *Runtime) linuxSandboxSetup(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	cwd string,
	env []string,
	spec codeexecutor.RunProgramSpec,
	mountProc bool,
) (linuxSandboxSetup, error) {
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
	} else {
		args = appendInaccessibleDirMaskArgs(args, "/proc")
	}
	if profile.network.Mode == NetworkRestricted {
		args = append(args, "--unshare-net")
	}
	grantArgs, err := r.externalGrantArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, grantArgs...)
	writeArgs, err := r.workspaceWriteMountArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, writeArgs...)
	protectedArgs, err := r.protectedMaskArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, protectedArgs...)
	readOnlyArgs, err := r.workspaceReadOnlyMountArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, readOnlyArgs...)
	denySetup, err := r.denyReadMaskSetup(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, denySetup.args...)
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
	return linuxSandboxSetup{
		args:                     args,
		syntheticDenyReadTargets: denySetup.syntheticTargets,
		needsDenyReadDataFD:      denySetup.needsBindDataFD,
	}, nil
}

func (r *Runtime) linuxPreflight() (string, bool, error) {
	r.preflightOnce.Do(func() {
		if r.backend != BackendAuto && r.backend != BackendLinuxBubblewrap {
			r.preflightErr = backendError(ErrUnsupportedBackend, string(r.backend), errors.New("unsupported backend on linux"))
			return
		}
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			r.preflightErr = backendError(ErrSetupFailed, string(BackendLinuxBubblewrap),
				errors.New("bubblewrap executable not found in PATH"))
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
			r.preflightErr = backendError(ErrSetupFailed, string(BackendLinuxBubblewrap), bwrapProbeError{
				err:    err,
				stderr: stderr,
			})
			return
		}
	})
	return r.bwrapPath, r.bwrapMountProc, r.preflightErr
}

// runBwrapPreflightProbe runs a short-lived bubblewrap probe and captures stderr.
//
// Strategy:
//   - linuxPreflight first runs /bin/true under bubblewrap with --proc /proc
//     and the same core namespace/mount flags used by real sandbox runs.
//   - The goal is to detect environments where mounting a fresh /proc fails, for
//     example restricted Docker-style containers, so the real run can retry
//     without --proc while keeping PID isolation.
//   - stderr is captured instead of streamed because this is a one-shot probe with
//     a trivial command and a short timeout.
func runBwrapPreflightProbe(bwrap string, mountProc bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := buildBwrapPreflightArgs(mountProc)
	var stderr bytes.Buffer
	probe := exec.CommandContext(ctx, bwrap, args...)
	probe.Stderr = &stderr
	err := probe.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stderr.String(), err
}

func buildBwrapPreflightArgs(mountProc bool) []string {
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
	} else {
		args = appendInaccessibleDirMaskArgs(args, "/proc")
	}
	args = append(args, "--", "/bin/true")
	return args
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
	for _, rel := range profile.fileSystem.ProtectedMetadata {
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
	for _, rel := range profile.fileSystem.ProtectedMetadata {
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
	setup, err := r.denyReadMaskSetup(profile, ws)
	if err != nil {
		return nil, err
	}
	return setup.args, nil
}

const denyReadBindDataFD = "3"

type denyReadMaskSetup struct {
	args             []string
	syntheticTargets []string
	needsBindDataFD  bool
}

func (r *Runtime) denyReadMaskSetup(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) (denyReadMaskSetup, error) {
	if err := r.validateNoAccessMasksEnforceable(profile, ws); err != nil {
		return denyReadMaskSetup{}, err
	}
	matches, err := r.deniedReadMatches(profile, ws)
	if err != nil {
		return denyReadMaskSetup{}, err
	}
	var args []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return denyReadMaskSetup{}, err
		}
		if info.IsDir() {
			args = appendInaccessibleDirMaskArgs(args, match)
			continue
		}
		args = append(args, "--ro-bind", denyReadMaskSource(ws), match)
	}
	syntheticTargets, err := r.missingNoAccessPathMaskTargets(profile, ws)
	if err != nil {
		return denyReadMaskSetup{}, err
	}
	for _, target := range syntheticTargets {
		args = append(args, "--perms", "000", "--ro-bind-data", denyReadBindDataFD, target)
	}
	return denyReadMaskSetup{
		args:             args,
		syntheticTargets: syntheticTargets,
		needsBindDataFD:  len(syntheticTargets) != 0,
	}, nil
}

func appendInaccessibleDirMaskArgs(args []string, target string) []string {
	return append(args,
		"--perms", "000",
		"--tmpfs", target,
		"--remount-ro", target,
	)
}

func denyReadMaskSource(ws codeexecutor.Workspace) string {
	return filepath.Join(ws.Path, ".trpc-agent-sandbox", "deny-read-mask")
}

func cleanupSyntheticDenyReadMaskTargets(targets []string) {
	for i := len(targets) - 1; i >= 0; i-- {
		info, err := os.Lstat(targets[i])
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			continue
		}
		_ = os.Remove(targets[i])
	}
}

func (r *Runtime) validateNoAccessMasksEnforceable(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) error {
	if err := validateFileSystemRules(profile); err != nil {
		return err
	}
	writeTargets, err := r.linuxWriteMountTargets(profile, ws)
	if err != nil {
		return err
	}
	if len(writeTargets) == 0 {
		return nil
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return err
	}
	writeRels := workspaceRelativeMounts(wsAbs, writeTargets)
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessNone {
			continue
		}
		switch rule.Kind {
		case ruleGlob:
			glob := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rule.Glob)))
			if glob == "" || glob == "." {
				continue
			}
			if strings.HasPrefix(glob, "../") || filepath.IsAbs(glob) {
				return deniedf(
					ErrPolicyViolation,
					"no-access-glob",
					rule.Glob,
					"linux backend requires workspace-relative glob denials",
				)
			}
			for _, writeRel := range writeRels {
				if globMayMatchUnder(glob, writeRel) {
					return deniedf(
						ErrPolicyViolation,
						"no-access-glob",
						rule.Glob,
						"glob denial overlaps writable mount %s and cannot be enforced after sandbox start",
						writeRel,
					)
				}
			}
		}
	}
	return nil
}

func (r *Runtime) missingNoAccessPathMaskTargets(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	writeTargets, err := r.linuxWriteMountTargets(profile, ws)
	if err != nil {
		return nil, err
	}
	if len(writeTargets) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var targets []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessNone || rule.Kind != rulePath {
			continue
		}
		target, ok, err := r.missingNoAccessPathMaskTarget(ws, writeTargets, rule.Path)
		if err != nil {
			return nil, err
		}
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	return targets, nil
}

func (r *Runtime) missingNoAccessPathMaskTarget(
	ws codeexecutor.Workspace,
	writeTargets []string,
	path string,
) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	target := path
	if !filepath.IsAbs(target) {
		resolved, _, err := r.resolveWorkspacePath(ws, target)
		if err != nil {
			return "", false, err
		}
		target = resolved
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return "", false, err
	}
	firstMissing, ok, err := firstMissingPathComponent(target)
	if err != nil || !ok {
		return "", false, err
	}
	for _, writeTarget := range writeTargets {
		if sameOrChild(writeTarget, firstMissing) {
			return firstMissing, true, nil
		}
	}
	return "", false, nil
}

func firstMissingPathComponent(target string) (string, bool, error) {
	target = filepath.Clean(target)
	if _, err := os.Lstat(target); err == nil {
		return "", false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	if !filepath.IsAbs(target) {
		return "", false, deniedf(
			ErrPathDenied,
			"no-access-path",
			target,
			"missing path target must be absolute",
		)
	}
	cur := string(os.PathSeparator)
	parts := strings.Split(strings.TrimPrefix(target, string(os.PathSeparator)), string(os.PathSeparator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		if _, err := os.Lstat(cur); err != nil {
			if os.IsNotExist(err) {
				return cur, true, nil
			}
			return "", false, err
		}
	}
	return "", false, nil
}

func (r *Runtime) linuxWriteMountTargets(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	targets, err := r.workspaceMountTargets(profile, ws, accessWrite)
	if err != nil {
		return nil, err
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var all []string
	for _, target := range targets {
		target, err = filepath.Abs(target)
		if err != nil {
			return nil, err
		}
		seen[target] = true
		all = append(all, target)
	}
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessWrite || rule.Kind != rulePath || rule.Path == "" ||
			!filepath.IsAbs(rule.Path) {
			continue
		}
		target, err := filepath.Abs(rule.Path)
		if err != nil {
			return nil, err
		}
		if sameOrChild(wsAbs, target) || seen[target] {
			continue
		}
		seen[target] = true
		all = append(all, target)
	}
	return all, nil
}

func workspaceRelativeMounts(wsAbs string, targets []string) []string {
	var rels []string
	for _, target := range targets {
		rel, err := filepath.Rel(wsAbs, target)
		if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
			continue
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		rels = append(rels, rel)
	}
	return rels
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
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != rulePath || rule.Path == "" || !filepath.IsAbs(rule.Path) {
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
		case accessRead:
			args = append(args, "--ro-bind", target, target)
		case accessWrite:
			args = append(args, "--bind", target, target)
		}
	}
	return args, nil
}

func (r *Runtime) workspaceWriteMountArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	targets, err := r.workspaceMountTargets(profile, ws, accessWrite)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, target := range targets {
		if _, err := os.Stat(target); err != nil {
			return nil, deniedf(ErrPathDenied, "grant", target, "workspace write grant target unavailable")
		}
		args = append(args, "--bind", target, target)
	}
	return args, nil
}

func (r *Runtime) workspaceReadOnlyMountArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	targets, err := r.workspaceMountTargets(profile, ws, accessRead)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, target := range targets {
		if target == ws.Path {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		args = append(args, "--ro-bind", target, target)
	}
	return args, nil
}

func (r *Runtime) workspaceMountTargets(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	access fileSystemAccess,
) ([]string, error) {
	if err := validateFileSystemRules(profile); err != nil {
		return nil, err
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var targets []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != access {
			continue
		}
		target, ok, err := r.workspaceMountTarget(ws, wsAbs, rule)
		if err != nil {
			return nil, err
		}
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	return targets, nil
}

func (r *Runtime) workspaceMountTarget(
	ws codeexecutor.Workspace,
	wsAbs string,
	rule fileSystemRule,
) (string, bool, error) {
	switch rule.Kind {
	case rulePath:
		if rule.Path == "" {
			return "", false, nil
		}
		if filepath.IsAbs(rule.Path) {
			target, err := filepath.Abs(rule.Path)
			if err != nil {
				return "", false, err
			}
			if !sameOrChild(wsAbs, target) {
				return "", false, nil
			}
			if err := ensureNoSymlinkEscape(wsAbs, target); err != nil {
				return "", false, err
			}
			return target, true, nil
		}
		target, _, err := r.resolveWorkspacePath(ws, rule.Path)
		if err != nil {
			return "", false, err
		}
		return target, true, nil
	case ruleSpecial:
		if rule.Special == specialRoot {
			if rule.Access == accessWrite {
				return "", false, deniedf(
					ErrPolicyViolation,
					"grant",
					string(rule.Special),
					"linux backend cannot grant managed write access to filesystem root",
				)
			}
			return "", false, nil
		}
		target, ok, err := specialPathAbs(ws, rule.Special)
		if err != nil || !ok {
			return "", false, err
		}
		if !sameOrChild(wsAbs, target) {
			return "", false, nil
		}
		return target, true, nil
	default:
		return "", false, nil
	}
}
