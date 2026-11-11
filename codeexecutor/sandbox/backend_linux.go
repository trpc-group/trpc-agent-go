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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

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
	// Append order must match linuxSandboxSetup descriptor numbering: ExtraFiles[i]
	// becomes child FD 3+i (seccomp then deny-read /dev/null when both are needed).
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
		"--cap-drop", "ALL",
		"--unshare-pid",
		"--new-session",
	}
	exposeHostRoot := profile.exposesHostRoot()
	if exposeHostRoot {
		args = append(args, "--ro-bind", "/", "/")
	}
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	if err := r.validateLinuxProtectedSymlinkEntries(profile, ws); err != nil {
		return linuxSandboxSetup{}, err
	}
	if !exposeHostRoot {
		args = append(args, "--tmpfs", "/tmp")
		seen := map[string]bool{"/": true, "/tmp": true}
		args = appendLinuxDirAncestors(args, wsAbs, seen)
		grantDests, err := r.linuxAbsoluteGrantDests(profile, ws)
		if err != nil {
			return linuxSandboxSetup{}, err
		}
		for _, dest := range grantDests {
			args = appendLinuxDirAncestors(args, dest, seen)
		}
	}
	needsSeccomp := profile.network.Mode != NetworkEnabled
	// ExtraFiles descriptors start at 3 and must match the append order in
	// openLinuxSandboxExtraFiles: seccomp memfd first, then deny-read /dev/null.
	nextExtraFD := 3
	if needsSeccomp {
		args = append(args, "--unshare-net", "--seccomp", strconv.Itoa(nextExtraFD))
		nextExtraFD++
	}
	grantArgs, err := r.externalGrantArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, grantArgs...)
	// Parent grants must not replace the private device or PID namespace views.
	args = append(args, linuxSystemViewArgs(profile, ws, mountProc)...)
	// Hide sessions after external parent binds, then restore only this scope.
	hideArgs, err := r.linuxSessionHideArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, hideArgs...)
	workspaceArgs, err := r.linuxWorkspaceMountArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, workspaceArgs...)
	credArgs, err := r.defaultCredentialDenyMaskArgs(profile, ws)
	if err != nil {
		return linuxSandboxSetup{}, err
	}
	args = append(args, credArgs...)
	denyReadFD := strconv.Itoa(nextExtraFD)
	denySetup, err := r.denyReadMaskSetup(profile, ws, denyReadFD)
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
		needsSeccompFD:           needsSeccomp,
		needsDenyReadDataFD:      denySetup.needsBindDataFD,
	}, nil
}

// linuxWorkspaceMountArgs restores the workspace baseline, applies effective
// carve-outs in every bind view, and finally protects its metadata.
func (r *Runtime) linuxWorkspaceMountArgs(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	baseArgs, err := r.workspaceRootReadOnlyMountArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	args := append([]string(nil), baseArgs...)
	writeArgs, err := r.workspaceWriteMountArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	protectedArgs, err := r.protectedMaskArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	readOnlyArgs, err := r.workspaceReadOnlyMountArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	workspaceArgs := linuxOrderedMountArgs(append(writeArgs, readOnlyArgs...))
	args = append(args, workspaceArgs...)
	aliasArgs, err := r.linuxAliasMountArgs(profile, ws, workspaceArgs, false)
	if err != nil {
		return nil, err
	}
	args = append(args, aliasArgs...)
	args = append(args, protectedArgs...)
	aliasProtectedArgs, err := r.linuxAliasMountArgs(profile, ws, protectedArgs, true)
	if err != nil {
		return nil, err
	}
	args = append(args, aliasProtectedArgs...)
	return args, nil
}

// validateLinuxProtectedSymlinkEntries rejects layouts where a content bind
// would leave a symlink in a protected path mutable. Binds follow links; making
// a link's parent read-only would also remove unrelated caller write grants.
func (r *Runtime) validateLinuxProtectedSymlinkEntries(profile PermissionProfile, ws codeexecutor.Workspace) error {
	paths, roots, err := r.linuxProtectedPathRoots(profile, ws)
	if err != nil {
		return err
	}
	readOnlyPaths, err := r.linuxReadOnlyRulePaths(profile, ws)
	if err != nil {
		return err
	}
	paths = append(paths, readOnlyPaths...)
	credentials, err := r.linuxProtectedCredentialPaths(profile, ws)
	if err != nil {
		return err
	}
	paths = append(paths, credentials...)
	seen := map[string]bool{}
	for _, path := range paths {
		for entry := filepath.Clean(path); entry != filepath.Dir(entry); entry = filepath.Dir(entry) {
			if seen[entry] {
				break
			}
			seen[entry] = true
			info, err := os.Lstat(entry)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			parent := policyCanonicalPath(filepath.Dir(entry))
			rel, err := filepath.Rel(ws.Path, parent)
			if err != nil {
				return err
			}
			access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), parent)
			if err != nil {
				return err
			}
			if !accessCanWrite(access) || linuxSymlinkParentProtected(profile, parent, roots, credentials) {
				continue
			}
			return deniedf(ErrPolicyViolation, "protect", entry,
				"linux backend cannot protect a symbolic link entry beneath writable parent %s", parent)
		}
	}
	return nil
}

func (r *Runtime) linuxProtectedPathRoots(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, []string, error) {
	paths, err := r.deniedReadMatches(profile, ws)
	if err != nil {
		return nil, nil, err
	}
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind == rulePath && rule.Access == accessNone && rule.Path != "" {
			path := rule.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(ws.Path, path)
			}
			paths = append(paths, path)
		}
	}
	for _, rel := range profile.fileSystem.ProtectedMetadata {
		rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel != "" && rel != "." {
			paths = append(paths, filepath.Join(ws.Path, filepath.FromSlash(rel)))
		}
	}
	var roots []string
	for _, path := range paths {
		roots = append(roots, policyCanonicalPath(path))
	}
	return paths, roots, nil
}

func (r *Runtime) linuxReadOnlyRulePaths(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	var paths []string
	for _, rule := range profile.fileSystem.Rules {
		if rule.Access != accessRead {
			continue
		}
		var path string
		switch rule.Kind {
		case rulePath:
			path = rule.Path
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(ws.Path, path)
			}
		case ruleSpecial:
			var ok bool
			var err error
			path, ok, err = specialPathAbs(ws, rule.Special)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		default:
			continue
		}
		source := policyCanonicalPath(path)
		rel, err := filepath.Rel(ws.Path, source)
		if err != nil {
			return nil, err
		}
		access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), source)
		if err != nil {
			return nil, err
		}
		if access == accessRead {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (r *Runtime) linuxProtectedCredentialPaths(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	var credentials []string
	workspace := policyCanonicalPath(ws.Path)
	for _, path := range defaultCredentialDenyPaths() {
		source := policyCanonicalPath(path)
		if sameOrChild(workspace, source) {
			continue
		}
		rel, err := filepath.Rel(ws.Path, source)
		if err != nil {
			return nil, err
		}
		access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), source)
		if err != nil {
			return nil, err
		}
		if accessCanWrite(access) && credentialExplicitlyGranted(profile, path, path) {
			continue
		}
		credentials = append(credentials, path)
	}
	return credentials, nil
}

// An enclosing protected mount already makes the symlink entry immutable.
func linuxSymlinkParentProtected(profile PermissionProfile, parent string, roots, credentials []string) bool {
	for _, root := range roots {
		if sameOrChild(root, parent) {
			return true
		}
	}
	for _, root := range credentials {
		if sameOrChild(policyCanonicalPath(root), parent) &&
			!credentialExplicitlyGranted(profile, root, parent) {
			return true
		}
	}
	return false
}

func linuxSystemViewArgs(profile PermissionProfile, ws codeexecutor.Workspace, mountProc bool) []string {
	var args []string
	for _, target := range protectedViewTargets(profile, ws, "/dev") {
		source := policyCanonicalPath(target)
		if source != "/dev" && sameOrChild("/dev", source) {
			continue
		}
		args = append(args, "--dev", target)
	}
	for _, target := range protectedViewTargets(profile, ws, "/proc") {
		source := policyCanonicalPath(target)
		if source != "/proc" && sameOrChild("/proc", source) {
			continue
		}
		if mountProc {
			args = append(args, "--proc", target)
		} else {
			args = appendInaccessibleDirMaskArgs(args, target)
		}
	}
	return args
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
		"--cap-drop", "ALL",
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
		abs, _, err := r.resolveWorkspacePath(ws, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			if err := os.MkdirAll(abs, 0o555); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) protectedMaskArgs(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	readTargets, err := r.workspaceMountTargets(profile, ws, accessRead)
	if err != nil {
		return nil, err
	}
	writeTargets, err := r.workspaceMountTargets(profile, ws, accessWrite)
	if err != nil {
		return nil, err
	}
	var args []string
	seen := map[string]bool{}
	for _, rel := range profile.fileSystem.ProtectedMetadata {
		rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
		if rel == "" || rel == "." {
			continue
		}
		root, _, err := r.resolveWorkspacePath(ws, filepath.FromSlash(rel))
		if err != nil {
			return nil, err
		}
		access, _, err := r.resolveAccess(profile, ws, rel, root)
		if err != nil {
			return nil, err
		}
		targets := []string{root}
		if !accessCanRead(access) {
			targets = nil
			for _, target := range append(readTargets, writeTargets...) {
				if sameOrChild(root, target) {
					targets = append(targets, target)
				}
			}
		}
		for _, target := range targets {
			if seen[target] {
				continue
			}
			if _, err := os.Stat(target); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			seen[target] = true
			args = append(args, "--ro-bind", target, policyCanonicalPath(target))
		}
	}
	return args, nil
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
	if err := r.validateDeniedReadChildGrants(profile, ws, matches); err != nil {
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
		for _, match := range protectedViewTargets(profile, ws, match) {
			if info.IsDir() {
				args = appendInaccessibleDirMaskArgs(args, match)
				continue
			}
			args = append(args, "--ro-bind", denyReadMaskSource(ws), match)
		}
	}
	syntheticTargets, err := r.missingNoAccessPathMaskTargets(profile, ws)
	if err != nil {
		return denyReadMaskSetup{}, err
	}
	for _, target := range syntheticTargets {
		for _, view := range protectedViewTargets(profile, ws, target) {
			args = append(args, "--perms", "000", "--ro-bind-data", bindDataFD, view)
		}
	}
	return denyReadMaskSetup{
		args:             args,
		syntheticTargets: syntheticTargets,
		needsBindDataFD:  len(syntheticTargets) != 0,
	}, nil
}

// validateDeniedReadChildGrants rejects grants that a final parent mask would
// hide. Restoring a child through an inaccessible mount requires a different
// namespace layout; silently accepting it would contradict rule specificity.
func (r *Runtime) validateDeniedReadChildGrants(profile PermissionProfile, ws codeexecutor.Workspace, denied []string) error {
	if len(denied) == 0 {
		return nil
	}
	for i, parent := range denied {
		denied[i] = policyCanonicalPath(parent)
	}
	for _, rule := range profile.fileSystem.Rules {
		if !accessCanRead(rule.Access) {
			continue
		}
		var target string
		if rule.Kind == ruleSpecial {
			path, ok, err := specialPathAbs(ws, rule.Special)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			target = path
		} else {
			target = strings.TrimSpace(rule.Path)
			if target == "" {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(ws.Path, target)
			}
		}
		rel, err := filepath.Rel(ws.Path, target)
		if err != nil {
			return err
		}
		access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), target)
		if err != nil {
			return err
		}
		if !accessCanRead(access) {
			continue
		}
		target = policyCanonicalPath(target)
		for _, parent := range denied {
			if parent != target && sameOrChild(parent, target) {
				return deniedf(ErrPolicyViolation, "grant", target,
					"linux backend cannot restore a child grant beneath no-access mask %s", parent)
			}
		}
	}
	return nil
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

// linuxExternalRules normalizes external path rules for mount destinations,
// external binds, and credential child restoration.
func linuxExternalRules(profile PermissionProfile, ws codeexecutor.Workspace) ([]fileSystemRule, error) {
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	var grants []fileSystemRule
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != rulePath || !filepath.IsAbs(rule.Path) {
			continue
		}
		rule.Path = filepath.Clean(rule.Path)
		if !sameOrChild(wsAbs, rule.Path) {
			grants = append(grants, rule)
		}
	}
	sort.SliceStable(grants, func(i, j int) bool {
		a, b := pathSpecificity(policyCanonicalPath(grants[i].Path)), pathSpecificity(policyCanonicalPath(grants[j].Path))
		if a != b {
			return a < b
		}
		return accessPrecedence(grants[i].Access) < accessPrecedence(grants[j].Access)
	})
	return grants, nil
}

func (r *Runtime) externalGrantArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	grants, err := linuxExternalRules(profile, ws)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, grant := range grants {
		if _, err := os.Stat(grant.Path); err != nil {
			if grant.optional && os.IsNotExist(err) {
				continue
			}
			return nil, deniedf(ErrPathDenied, "grant", grant.Path, "external grant target unavailable")
		}
		if accessCanRead(grant.Access) {
			source := policyCanonicalPath(grant.Path)
			rel, err := filepath.Rel(ws.Path, source)
			if err != nil {
				return nil, err
			}
			effective, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), source)
			if err != nil {
				return nil, err
			}
			if !accessCanRead(effective) {
				continue
			}
			destination := linuxGrantDestination(profile, ws, grant.Path)
			bind := "--ro-bind"
			if accessCanWrite(effective) {
				bind = "--bind"
			}
			if grant.optional {
				bind += "-try"
			}
			args = append(args, bind, grant.Path, destination)
		}
	}
	aliases, err := r.linuxAliasMountArgs(profile, ws, args, false)
	if err != nil {
		return nil, err
	}
	return linuxOrderedMountArgs(append(args, aliases...)), nil
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
		args = append(args, "--bind", target, linuxGrantDestination(profile, ws, target))
	}
	return args, nil
}

// workspaceRootReadOnlyMountArgs establishes the workspace baseline when the
// host root is absent. Writable mounts and read-only carve-outs are added later.
func (r *Runtime) workspaceRootReadOnlyMountArgs(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	access, _, err := r.resolveAccess(profile, ws, ".", wsAbs)
	if err != nil || !accessCanRead(access) {
		return nil, err
	}
	return []string{"--ro-bind", wsAbs, linuxGrantDestination(profile, ws, wsAbs)}, nil
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
		args = append(args, "--ro-bind", target, linuxGrantDestination(profile, ws, target))
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
		rel, err := filepath.Rel(wsAbs, target)
		if err != nil {
			return nil, err
		}
		effective, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), target)
		if err != nil {
			return nil, err
		}
		if effective != access {
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
				workspace := policyCanonicalPath(wsAbs)
				canonical := policyCanonicalPath(target)
				if !sameOrChild(workspace, canonical) {
					return "", false, nil
				}
				// An external alias can constrain a workspace resource. Apply
				// its effective rule after the broader workspace mounts too.
				rel, err := filepath.Rel(workspace, canonical)
				if err != nil {
					return "", false, err
				}
				target = filepath.Join(wsAbs, rel)
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
		if err := ensureNoSymlinkEscape(wsAbs, target); err != nil {
			return "", false, err
		}
		return target, true, nil
	default:
		return "", false, nil
	}
}

func platformReadPaths() []string {
	return []string{
		"/usr",
		"/bin",
		"/sbin",
		"/lib",
		"/lib64",
		"/lib32",
		// Only public runtime configuration is shared. In particular, do not
		// expose /etc/ssl/private, application configuration, or host credentials.
		"/etc/ld.so.cache",
		"/etc/ld.so.conf",
		"/etc/ld.so.conf.d",
		// Debian-family command shims such as /usr/bin/awk resolve here.
		"/etc/alternatives",
		"/etc/passwd",
		"/etc/group",
		"/etc/nsswitch.conf",
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/services",
		"/etc/protocols",
		"/etc/shells",
		"/etc/localtime",
		"/etc/ssl/certs",
		"/etc/pki/tls/certs",
		"/etc/pki/ca-trust/extracted",
	}
}

func appendLinuxDirAncestors(args []string, dest string, seen map[string]bool) []string {
	dest = filepath.Clean(dest)
	if dest == "" || dest == string(os.PathSeparator) {
		return args
	}
	var ancestors []string
	cur := dest
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		ancestors = append([]string{parent}, ancestors...)
		cur = parent
	}
	for _, parent := range ancestors {
		if parent == string(os.PathSeparator) || seen[parent] {
			continue
		}
		seen[parent] = true
		args = append(args, "--dir", parent)
	}
	return args
}

func (r *Runtime) linuxAbsoluteGrantDests(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	grants, err := linuxExternalRules(profile, ws)
	if err != nil {
		return nil, err
	}
	var dests []string
	for _, grant := range grants {
		if grant.Access == accessRead || grant.Access == accessWrite {
			dests = append(dests, linuxGrantDestination(profile, ws, grant.Path))
		}
	}
	return dests, nil
}

func (r *Runtime) defaultCredentialDenyMaskArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	grantArgs, err := r.defaultCredentialGrantArgs(profile, ws)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	var args []string
	for _, path := range defaultCredentialDenyPaths() {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if skipDefaultCredentialDeny(profile, abs) {
			continue
		}
		if sameOrChild(wsAbs, abs) {
			continue
		}
		if skipGrantedHomeCredentialMask(profile, home, abs) {
			continue
		}
		maskTarget, info, err := credentialMaskTarget(abs)
		if err != nil {
			continue
		}
		if sameOrChild(wsAbs, maskTarget) {
			continue
		}
		for _, viewTarget := range protectedViewTargets(profile, ws, abs) {
			if credentialExplicitlyGranted(profile, abs, viewTarget) {
				continue
			}
			maskTarget = viewTarget
			if info.IsDir() {
				perms := "000"
				if hasCredentialChildGrant(grantArgs, maskTarget) {
					// Traversable but not listable so granted children stay reachable.
					perms = "0111"
				}
				args = append(args, "--perms", perms, "--tmpfs", maskTarget)
				for i := 0; i+2 < len(grantArgs); i += 3 {
					if grantArgs[i+2] != maskTarget && sameOrChild(maskTarget, grantArgs[i+2]) {
						args = append(args, grantArgs[i], grantArgs[i+1], grantArgs[i+2])
					}
				}
				args = append(args, "--remount-ro", maskTarget)
				continue
			}
			args = append(args, "--ro-bind", denyReadMaskSource(ws), maskTarget)
		}
	}
	return args, nil
}

func credentialMaskTarget(path string) (string, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	return filepath.Clean(resolved), info, nil
}

func hasCredentialChildGrant(grantArgs []string, parent string) bool {
	for i := 0; i+2 < len(grantArgs); i += 3 {
		target := grantArgs[i+2]
		if target != parent && sameOrChild(parent, target) {
			return true
		}
	}
	return false
}

// defaultCredentialGrantArgs rebinds an explicitly granted child after the
// parent tmpfs and before --remount-ro, without exposing siblings.
func (r *Runtime) defaultCredentialGrantArgs(
	profile PermissionProfile,
	ws codeexecutor.Workspace,
) ([]string, error) {
	wsAbs, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, err
	}
	grants, err := linuxExternalRules(profile, ws)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var args []string
	for _, rule := range grants {
		target := rule.Path
		parent, ok := credentialDenyParent(target)
		if seen[target] || !ok ||
			skipGrantedHomeCredentialMask(profile, home, parent) {
			continue
		}
		source := policyCanonicalPath(target)
		rel, err := filepath.Rel(wsAbs, source)
		if err != nil {
			return nil, err
		}
		access, matched, err := r.resolveAccess(profile, ws, rel, source)
		if err != nil {
			return nil, err
		}
		if !matched || access == accessNone {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			return nil, deniedf(ErrPathDenied, "grant", target, "external grant target unavailable")
		}
		seen[target] = true
		for _, dest := range protectedViewTargets(profile, ws, target) {
			bind := "--ro-bind"
			if accessCanWrite(access) {
				bind = "--bind"
			}
			args = append(args, bind, target, dest)
		}
	}
	return args, nil
}

func credentialDenyParent(target string) (string, bool) {
	target = policyCanonicalPath(target)
	for _, path := range defaultCredentialDenyPaths() {
		cred, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		canonical := policyCanonicalPath(cred)
		if canonical != target && sameOrChild(canonical, target) {
			return cred, true
		}
	}
	return "", false
}

func (r *Runtime) linuxSessionHideArgs(profile PermissionProfile, ws codeexecutor.Workspace) ([]string, error) {
	// Preserve error reporting for a relative runtime root after cwd disappears.
	if _, err := filepath.Abs(r.root); err != nil {
		return nil, err
	}
	sessions := r.sessionProtectionRoot(ws)
	if sessions == "" {
		return nil, nil
	}
	var args []string
	for _, target := range protectedViewTargets(profile, ws, sessions) {
		if sameOrChild(policyCanonicalPath(ws.Path), policyCanonicalPath(target)) {
			continue
		}
		args = append(args, "--tmpfs", target)
	}
	if profile.exposesHostRoot() {
		return args, nil
	}
	// A parent grant may introduce an alternate spelling of this workspace.
	// Restore just the current subtree, with its effective read/write access.
	grants, err := linuxExternalRules(profile, ws)
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		source := policyCanonicalPath(grant.Path)
		workspace := policyCanonicalPath(ws.Path)
		if !accessCanRead(grant.Access) || !sameOrChild(source, workspace) {
			continue
		}
		target, _ := grantViewTarget(source, linuxGrantDestination(profile, ws, grant.Path), workspace)
		if target == ws.Path {
			continue
		}
		bind := "--ro-bind"
		access, _, err := r.resolveAccess(profile, ws, ".", ws.Path)
		if err != nil {
			return nil, err
		}
		if accessCanWrite(access) {
			bind = "--bind"
		}
		args = append(args, bind, ws.Path, target)
	}
	return args, nil
}

// linuxAliasMountArgs repeats effective mounts at the bind views exposing the
// same resource. Clipped grants keep their own source subtree, never their parent.
func (r *Runtime) linuxAliasMountArgs(profile PermissionProfile, ws codeexecutor.Workspace, mounts []string, protected bool) ([]string, error) {
	if profile.exposesHostRoot() {
		return nil, nil
	}
	var args []string
	views := linuxGrantViews(profile, ws)
	for i := 0; i+2 < len(mounts); i += 3 {
		for _, view := range views {
			target, ok := grantViewTarget(view.source, view.destination, mounts[i+1])
			if !ok || target == mounts[i+2] {
				continue
			}
			mountSource, bind := mounts[i+1], mounts[i]
			if sameOrChild(policyCanonicalPath(mounts[i+1]), view.source) {
				mountSource = view.source
				if !protected {
					rel, err := filepath.Rel(ws.Path, mountSource)
					if err != nil {
						return nil, err
					}
					access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), mountSource)
					if err != nil {
						return nil, err
					}
					if !accessCanRead(access) {
						continue
					}
					bind = "--ro-bind"
					if accessCanWrite(access) {
						bind = "--bind"
					}
				}
			}
			args = append(args, bind, mountSource, target)
		}
	}
	return linuxOrderedMountArgs(args), nil
}

// linuxOrderedMountArgs places ancestors before their more-specific carveouts.
func linuxOrderedMountArgs(args []string) []string {
	var mounts [][3]string
	for i := 0; i+2 < len(args); i += 3 {
		mounts = append(mounts, [3]string{args[i], args[i+1], args[i+2]})
	}
	sort.SliceStable(mounts, func(i, j int) bool { return pathSpecificity(mounts[i][2]) < pathSpecificity(mounts[j][2]) })
	var ordered []string
	for _, mount := range mounts {
		ordered = append(ordered, mount[:]...)
	}
	return ordered
}

// linuxGrantDestination preserves a bind root's requested spelling in private
// views. An ancestor bind materializes its own root alias, but keeps descendant
// symlinks: grants through those links also need their real targets established.
func linuxGrantDestination(profile PermissionProfile, ws codeexecutor.Workspace, path string) string {
	if profile.exposesHostRoot() {
		return policyCanonicalPath(path)
	}
	path = filepath.Clean(path)
	var parent string
	for _, rule := range profile.fileSystem.Rules {
		if !accessCanRead(rule.Access) {
			continue
		}
		var target string
		switch rule.Kind {
		case rulePath:
			target = rule.Path
			if target == "" {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(ws.Path, target)
			}
		case ruleSpecial:
			var ok bool
			var err error
			target, ok, err = specialPathAbs(ws, rule.Special)
			if err != nil || !ok {
				continue
			}
		default:
			continue
		}
		target = filepath.Clean(target)
		if target != path && sameOrChild(target, path) && len(target) > len(parent) {
			parent = target
		}
	}
	if parent != "" {
		rel, _ := filepath.Rel(parent, path)
		underSource := filepath.Join(policyCanonicalPath(parent), rel)
		if policyCanonicalPath(underSource) != underSource {
			return policyCanonicalPath(path)
		}
	}
	return path
}

type linuxGrantView struct {
	source      string
	destination string
}

// linuxGrantViews describes the host resources exposed by explicit binds and
// the workspace baseline, including when the workspace root itself is an alias.
func linuxGrantViews(profile PermissionProfile, ws codeexecutor.Workspace) []linuxGrantView {
	views := []linuxGrantView{{
		source:      policyCanonicalPath(ws.Path),
		destination: linuxGrantDestination(profile, ws, ws.Path),
	}}
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != rulePath || !filepath.IsAbs(rule.Path) || !accessCanRead(rule.Access) {
			continue
		}
		if rule.optional {
			if _, err := os.Stat(rule.Path); err != nil {
				continue
			}
		}
		views = append(views, linuxGrantView{
			source:      policyCanonicalPath(rule.Path),
			destination: linuxGrantDestination(profile, ws, rule.Path),
		})
	}
	return views
}

// protectedViewTargets includes every bind view exposing a protected resource.
// Protections are applied after all grants so parent binds cannot replace them.
func protectedViewTargets(profile PermissionProfile, ws codeexecutor.Workspace, target string) []string {
	targets := []string{policyCanonicalPath(target)}
	// Host views preserve symlinks and bind grants at their canonical targets.
	if profile.exposesHostRoot() {
		return targets
	}
	seen := map[string]bool{targets[0]: true}
	for _, view := range linuxGrantViews(profile, ws) {
		mapped, ok := grantViewTarget(view.source, view.destination, target)
		if ok && !seen[mapped] {
			targets = append(targets, mapped)
			seen[mapped] = true
		}
	}
	return targets
}

// grantViewTarget maps a host subtree to a bind destination. A grant directly
// inside the subtree maps to its own destination instead of exposing its parent.
func grantViewTarget(source, destination, target string) (string, bool) {
	source, target = policyCanonicalPath(source), policyCanonicalPath(target)
	if sameOrChild(source, target) {
		rel, _ := filepath.Rel(source, target)
		return filepath.Join(destination, rel), true
	}
	if sameOrChild(target, source) {
		return destination, true
	}
	return "", false
}
