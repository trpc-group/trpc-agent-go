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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	_ = diagnostics
	bwrap, mountProc, err := r.linuxPreflight(ctx)
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	if profile.network.Mode != NetworkEnabled {
		if err := r.linuxRestrictedPreflight(ctx, bwrap, mountProc); err != nil {
			return nil, string(BackendLinuxBubblewrap), nil, err
		}
	}
	if err := r.prepareProtectedMasks(profile, ws); err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	setup, err := r.linuxSandboxSetup(profile, ws, cwd, env, spec, mountProc)
	if err != nil {
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	cmd := exec.CommandContext(ctx, bwrap, setup.args...)
	extraFiles, cleanup, err := linuxOpenExtraFiles(setup)
	if err != nil {
		cleanupSyntheticDenyReadMaskTargets(setup.syntheticDenyReadTargets)
		return nil, string(BackendLinuxBubblewrap), nil, err
	}
	cmd.ExtraFiles = extraFiles
	return cmd, string(BackendLinuxBubblewrap), cleanup, nil
}

// Dependencies for restricted seccomp setup. Tests override these to exercise
// fail-closed branches without mocking the kernel or bubblewrap binary.
var (
	linuxNativeSeccompPolicy = nativeSeccompPolicy
	linuxKernelRelease       = currentKernelRelease
	linuxOpenSeccompMemfd    = openSeccompFilterMemfd
	linuxSeccompProbe        = runBwrapSeccompPreflightProbe
	linuxOpenExtraFiles      = openLinuxSandboxExtraFiles
	linuxBasePreflightProbe  = runBwrapPreflightProbe
)

type linuxSandboxSetup struct {
	args                     []string
	syntheticDenyReadTargets []string
	needsSeccompFD           bool
	needsDenyReadDataFD      bool
	denyReadBindDataFD       string
}

func openLinuxSandboxExtraFiles(setup linuxSandboxSetup) ([]*os.File, commandCleanup, error) {
	var files []*os.File
	closeAll := func() {
		for i, f := range files {
			if f == nil {
				continue
			}
			_ = f.Close()
			files[i] = nil
		}
	}
	// Append order must match linuxSandboxSetup descriptor numbering:
	// ExtraFiles[i] becomes child FD 3+i (seccomp, then deny-read /dev/null).
	if setup.needsSeccompFD {
		seccompFile, err := openSeccompFilterMemfd()
		if err != nil {
			return nil, nil, backendError(
				ErrSetupFailed,
				string(BackendLinuxBubblewrap),
				err,
			)
		}
		files = append(files, seccompFile)
	}
	if setup.needsDenyReadDataFD {
		nullFile, err := os.Open("/dev/null")
		if err != nil {
			closeAll()
			return nil, nil, backendError(
				ErrSetupFailed,
				string(BackendLinuxBubblewrap),
				err,
			)
		}
		files = append(files, nullFile)
	}
	synthetic := append([]string(nil), setup.syntheticDenyReadTargets...)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			closeAll()
			cleanupSyntheticDenyReadMaskTargets(synthetic)
		})
	}
	if len(files) == 0 && len(synthetic) == 0 {
		return nil, nil, nil
	}
	if len(files) == 0 {
		return nil, cleanup, nil
	}
	return files, cleanup, nil
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
	plan, err := resolveNetworkPolicy(profile)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	needsSeccomp := plan.isolateNetwork
	// ExtraFiles descriptors start at 3 and must match the append order in
	// openLinuxSandboxExtraFiles: seccomp memfd first, then deny-read /dev/null.
	nextExtraFD := 3
	seccompFD := -1
	if needsSeccomp {
		seccompFD = nextExtraFD
		nextExtraFD++
		args = append(args, "--unshare-net")
		if plan.mode != NetworkControlled {
			args = append(args, "--seccomp", strconv.Itoa(seccompFD))
		}
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
	denyReadFD := strconv.Itoa(nextExtraFD)
	denySetup, err := r.denyReadMaskSetup(profile, ws, denyReadFD)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, denySetup.args...)
	if plan.mode == NetworkControlled {
		socketPath, err := r.validateControlledEgressSocketPath(profile, ws, plan.unixPath)
		if err != nil {
			return linuxSandboxSetup{}, err
		}
		plan.unixPath = socketPath
		relayPath := r.egressRelayPath
		if relayPath == "" {
			relayPath = os.Getenv("TRPC_AGENT_EGRESS_RELAY")
		}
		relayPath, err = r.validateControlledEgressRelayPath(profile, ws, relayPath)
		if err != nil {
			return linuxSandboxSetup{}, err
		}
		env = applyControlledEgressEnv(env, plan)
		spec, err = wrapControlledEgressSpec(
			plan,
			relayPath,
			r.bwrapPath,
			seccompFD,
			mountProc,
			spec,
		)
		if err != nil {
			return linuxSandboxSetup{}, err
		}
	}
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
		needsSeccompFD:           needsSeccomp,
		needsDenyReadDataFD:      denySetup.needsBindDataFD,
		denyReadBindDataFD:       denyReadFD,
	}, nil
}

// linuxPreflight verifies that this Runtime can start a bubblewrap sandbox.
// Durable capability results are cached for the Runtime lifetime. Caller
// cancellation and probe deadlines are not cached, so one transient request
// cannot permanently poison later sandbox runs.
func (r *Runtime) linuxPreflight(ctx context.Context) (string, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}

		r.preflightMu.Lock()
		if r.preflightReady {
			bwrap, mountProc, err := r.bwrapPath, r.bwrapMountProc, r.preflightErr
			r.preflightMu.Unlock()
			return bwrap, mountProc, err
		}
		if r.preflightWait != nil {
			done := r.preflightWait
			r.preflightMu.Unlock()
			select {
			case <-ctx.Done():
				return "", false, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		r.preflightWait = done
		r.preflightMu.Unlock()

		bwrap, mountProc, err := r.runLinuxPreflight(ctx)
		cache := !isTransientRestrictedPreflightError(err)

		r.preflightMu.Lock()
		if cache {
			r.preflightReady = true
			r.bwrapPath = bwrap
			r.bwrapMountProc = mountProc
			r.preflightErr = err
		}
		close(done)
		r.preflightWait = nil
		r.preflightMu.Unlock()
		return bwrap, mountProc, err
	}
}

func (r *Runtime) runLinuxPreflight(ctx context.Context) (string, bool, error) {
	if r.backend != BackendAuto && r.backend != BackendLinuxBubblewrap {
		return "", false, backendError(
			ErrUnsupportedBackend,
			string(r.backend),
			errors.New("unsupported backend on linux"),
		)
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "", false, backendError(
			ErrSetupFailed,
			string(BackendLinuxBubblewrap),
			errors.New("bubblewrap executable not found in PATH"),
		)
	}
	stderr, err := linuxBasePreflightProbe(ctx, bwrap, true)
	if err == nil {
		return bwrap, true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", false, ctxErr
	}
	if isProcMountFailure(stderr) {
		stderr, err = linuxBasePreflightProbe(ctx, bwrap, false)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", false, ctxErr
			}
			return "", false, backendError(
				ErrSetupFailed,
				string(BackendLinuxBubblewrap),
				bwrapProbeError{err: err, stderr: stderr},
			)
		}
		return bwrap, false, nil
	}
	return "", false, backendError(
		ErrSetupFailed,
		string(BackendLinuxBubblewrap),
		bwrapProbeError{err: err, stderr: stderr},
	)
}

// linuxRestrictedPreflight verifies that this Runtime can enforce restricted
// AF_UNIX seccomp. Durable capability results are cached for the Runtime
// lifetime. Caller cancellation and probe deadlines are not cached, so one
// transient request cannot permanently poison later restricted runs.
func (r *Runtime) linuxRestrictedPreflight(
	ctx context.Context,
	bwrap string,
	mountProc bool,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		r.restrictedPreflightMu.Lock()
		if r.restrictedPreflightReady {
			err := r.restrictedPreflightErr
			r.restrictedPreflightMu.Unlock()
			return err
		}
		if r.restrictedPreflightDone != nil {
			done := r.restrictedPreflightDone
			r.restrictedPreflightMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		r.restrictedPreflightDone = done
		r.restrictedPreflightMu.Unlock()

		err := runLinuxRestrictedPreflight(ctx, bwrap, mountProc)
		cache := !isTransientRestrictedPreflightError(err)

		r.restrictedPreflightMu.Lock()
		if cache {
			r.restrictedPreflightReady = true
			r.restrictedPreflightErr = err
		}
		close(done)
		r.restrictedPreflightDone = nil
		r.restrictedPreflightMu.Unlock()
		return err
	}
}

func isTransientRestrictedPreflightError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.EINTR) ||
		errors.Is(err, syscall.EAGAIN) ||
		errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOMEM)
}

func runLinuxRestrictedPreflight(
	ctx context.Context,
	bwrap string,
	mountProc bool,
) error {
	if _, err := linuxNativeSeccompPolicy(); err != nil {
		return backendError(
			ErrUnsupportedBackend,
			string(BackendLinuxBubblewrap),
			err,
		)
	}
	release, err := linuxKernelRelease()
	if err != nil {
		return backendError(
			ErrSetupFailed,
			string(BackendLinuxBubblewrap),
			fmt.Errorf("read kernel release: %w", err),
		)
	}
	if err := kernelSupportsRestrictedSeccomp(release); err != nil {
		return backendError(
			ErrSetupFailed,
			string(BackendLinuxBubblewrap),
			err,
		)
	}
	seccompFile, err := linuxOpenSeccompMemfd()
	if err != nil {
		return backendError(
			ErrSetupFailed,
			string(BackendLinuxBubblewrap),
			err,
		)
	}
	defer seccompFile.Close()
	stderr, err := linuxSeccompProbe(ctx, bwrap, mountProc, seccompFile)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return backendError(
			ErrSetupFailed,
			string(BackendLinuxBubblewrap),
			bwrapProbeError{
				err:    err,
				stderr: stderr,
				hint:   "restricted AF_UNIX seccomp preflight failed",
			},
		)
	}
	return nil
}

// runBwrapSeccompPreflightProbe verifies bubblewrap can load the AF_UNIX
// seccomp filter before a restricted sandbox command starts.
func runBwrapSeccompPreflightProbe(
	ctx context.Context,
	bwrap string,
	mountProc bool,
	seccompFile *os.File,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, bwrapSeccompPreflightTimeout)
	defer cancel()
	args := buildBwrapPreflightArgs(mountProc)
	// Insert --seccomp 3 before the final "--" /bin/true pair.
	args = append(args[:len(args)-2], "--seccomp", "3", "--", "/bin/true")
	var stderr bytes.Buffer
	probe := exec.CommandContext(ctx, bwrap, args...)
	probe.ExtraFiles = []*os.File{seccompFile}
	probe.Stderr = &stderr
	err := probe.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stderr.String(), err
}

// bwrapSeccompPreflightTimeout bounds the restricted seccomp bubblewrap probe.
// Tests may lower it to exercise the context-deadline fail-closed path.
var bwrapSeccompPreflightTimeout = 5 * time.Second

// bwrapBasePreflightTimeout bounds the bubblewrap capability probe used by
// linuxPreflight. The caller context can cancel sooner.
var bwrapBasePreflightTimeout = 5 * time.Second

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
func runBwrapPreflightProbe(ctx context.Context, bwrap string, mountProc bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, bwrapBasePreflightTimeout)
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
	setup, err := r.denyReadMaskSetup(profile, ws, "3")
	if err != nil {
		return nil, err
	}
	return setup.args, nil
}

type denyReadMaskSetup struct {
	args             []string
	syntheticTargets []string
	needsBindDataFD  bool
}

func (r *Runtime) denyReadMaskSetup(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	bindDataFD string,
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
		args = append(args, "--perms", "000", "--ro-bind-data", bindDataFD, target)
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

func (r *Runtime) validateControlledEgressSocketPath(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	unixPath string,
) (string, error) {
	socketAbs, err := filepath.Abs(unixPath)
	if err != nil {
		return "", deniedf(
			ErrPolicyViolation,
			"network",
			unixPath,
			"resolve controlled egress UnixPath: %v",
			err,
		)
	}
	writeTargets, err := r.linuxWriteMountTargets(profile, ws)
	if err != nil {
		return "", err
	}
	for _, target := range writeTargets {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		if sameOrChild(targetAbs, socketAbs) {
			return "", deniedf(
				ErrPolicyViolation,
				"network",
				socketAbs,
				"controlled egress UnixPath must be outside guest-writable mounts",
			)
		}
	}
	socketResolved, err := resolveControlledEgressSocketPath(socketAbs)
	if err != nil {
		return "", deniedf(
			ErrPolicyViolation,
			"network",
			socketAbs,
			"resolve controlled egress UnixPath symlinks: %v",
			err,
		)
	}
	for _, target := range writeTargets {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		targetResolved, err := filepath.EvalSymlinks(targetAbs)
		if err != nil {
			return "", deniedf(
				ErrPolicyViolation,
				"network",
				targetAbs,
				"resolve guest-writable mount symlinks: %v",
				err,
			)
		}
		if sameOrChild(targetResolved, socketResolved) {
			return "", deniedf(
				ErrPolicyViolation,
				"network",
				socketAbs,
				"controlled egress UnixPath must be outside guest-writable mounts",
			)
		}
	}
	return socketResolved, nil
}

func resolveControlledEgressSocketPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	current := path
	for i := 0; i < 255; i++ {
		target, changed, resolveErr := resolvePotentialSymlinkTarget(current)
		if resolveErr != nil {
			return "", resolveErr
		}
		if !changed {
			return current, nil
		}
		current = target
		resolved, err = filepath.EvalSymlinks(current)
		if err == nil {
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("too many symlinks resolving %q", path)
}

func (r *Runtime) validateControlledEgressRelayPath(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
	relayPath string,
) (string, error) {
	if relayPath == "" {
		return "", deniedf(
			ErrSetupFailed,
			"network",
			"",
			"controlled egress requires egress-relay helper (WithControlledEgressRelayPath or TRPC_AGENT_EGRESS_RELAY)",
		)
	}
	relayAbs, err := filepath.Abs(relayPath)
	if err != nil {
		return "", deniedf(ErrSetupFailed, "network", relayPath, "resolve relay path: %v", err)
	}
	relayResolved, err := filepath.EvalSymlinks(relayAbs)
	if err != nil {
		return "", deniedf(ErrSetupFailed, "network", relayAbs, "resolve relay path symlinks: %v", err)
	}
	st, err := os.Stat(relayResolved)
	if err != nil || !st.Mode().IsRegular() {
		return "", deniedf(ErrSetupFailed, "network", relayResolved, "egress-relay helper not found")
	}
	if st.Mode().Perm()&0o111 == 0 {
		return "", deniedf(ErrSetupFailed, "network", relayResolved, "egress-relay helper is not executable")
	}
	writeTargets, err := r.linuxWriteMountTargets(profile, ws)
	if err != nil {
		return "", err
	}
	for _, target := range writeTargets {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		targetResolved, err := filepath.EvalSymlinks(targetAbs)
		if err != nil {
			return "", deniedf(
				ErrPolicyViolation,
				"network",
				targetAbs,
				"resolve guest-writable mount symlinks: %v",
				err,
			)
		}
		if sameOrChild(targetAbs, relayAbs) ||
			sameOrChild(targetResolved, relayResolved) {
			return "", deniedf(
				ErrSetupFailed,
				"network",
				relayResolved,
				"egress-relay helper must be outside guest-writable mounts",
			)
		}
	}
	return relayResolved, nil
}
