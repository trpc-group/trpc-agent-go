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
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var _ codeexecutor.WorkspaceManager = (*Runtime)(nil)
var _ codeexecutor.WorkspaceFS = (*Runtime)(nil)
var _ codeexecutor.ProgramRunner = (*Runtime)(nil)
var _ codeexecutor.Engine = (*Runtime)(nil)

// Runtime implements workspace management, filesystem policy checks, and
// program execution for the sandbox executor.
type Runtime struct {
	root             string
	backend          BackendType
	profile          PermissionProfile
	sessionPolicy    SessionPolicy
	sessionPolicySet bool
	envPolicy        ShellEnvironmentPolicy
	manifest         Manifest
	outputMaxBytes   int
	defaultTimeout   time.Duration
	denials          any

	mu       sync.Mutex
	runLocks map[string]*workspaceRunLock

	preflightOnce  sync.Once
	preflightGate  chan struct{}
	preflightDone  bool
	preflightErr   error
	bwrapPath      string
	bwrapMountProc bool
	seatbeltPath   string
}

// NewRuntime constructs a sandbox runtime.
func NewRuntime(opts ...Option) *Runtime {
	r := &Runtime{
		root:           defaultWorkspaceRoot(),
		backend:        BackendAuto,
		profile:        WorkspaceWriteProfile(),
		sessionPolicy:  defaultSessionPolicy(),
		envPolicy:      normalizeShellEnvironmentPolicy(ShellEnvironmentPolicy{}),
		outputMaxBytes: defaultOutputMaxBytes,
		defaultTimeout: defaultRunTimeout,
		runLocks:       map[string]*workspaceRunLock{},
		preflightGate:  make(chan struct{}, 1),
	}
	r.preflightGate <- struct{}{}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	r.profile = normalizeProfile(r.profile)
	if !r.sessionPolicySet {
		r.sessionPolicy = defaultSessionPolicy()
	}
	r.envPolicy = normalizeShellEnvironmentPolicy(r.envPolicy)
	if r.outputMaxBytes <= 0 {
		r.outputMaxBytes = defaultOutputMaxBytes
	}
	if r.defaultTimeout <= 0 {
		r.defaultTimeout = defaultRunTimeout
	}
	r.applyManifestPolicy()
	r.initDenialDiagnosticsState()
	return r
}

// Manager returns the runtime as a workspace manager.
func (r *Runtime) Manager() codeexecutor.WorkspaceManager { return r }

// FS returns the runtime as a workspace filesystem.
func (r *Runtime) FS() codeexecutor.WorkspaceFS { return r }

// Runner returns the runtime as a program runner.
func (r *Runtime) Runner() codeexecutor.ProgramRunner { return r }

// Describe reports generic engine capabilities.
func (r *Runtime) Describe() codeexecutor.Capabilities {
	profile := normalizeProfile(r.profile)
	isolation := "os-sandbox"
	if profile.enforcement() == enforcementDisabled {
		isolation = "none"
	}
	if profile.enforcement() == enforcementExternal {
		isolation = "external"
	}
	return codeexecutor.Capabilities{
		Isolation:             isolation,
		NetworkAllowed:        profile.network.Mode == NetworkEnabled,
		ReadOnlyMount:         profile.enforcement() == enforcementManaged,
		Streaming:             false,
		SupportsDeclarativeIO: codeexecutor.SupportsDeclarativeIOTrue(),
	}
}

// CreateWorkspace creates or opens the deterministic directory for an
// execution/session id.
func (r *Runtime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	if execID == "" {
		execID = "default"
	}
	root, id := workspacePathForID(r.root, execID)
	// Upgrade a pre-encoding-change PerSession workspace before creating
	// the new directory; once root exists the legacy directory would be
	// orphaned.
	//
	// The primary trigger is shape-based so every framework caller is
	// covered without importing this package's private context key: the
	// flow processor, workspacesession.Resolver, and openclaw all pass
	// workspacesession.KeyFromInvocation(invocation) — i.e.
	// codeexecutor.SessionWorkspaceKey(app, user, id) — as the workspace
	// ID. When execID equals that value for the invocation's session,
	// derive the legacy key ("app/user/id" or "id") and migrate. The
	// context value (withLegacyWorkspaceKey) remains as an explicit
	// override; it must not be the only trigger.
	if err := r.migrateLegacyWorkspace(execID, resolveLegacyWorkspaceKey(ctx, execID)); err != nil {
		return codeexecutor.Workspace{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return codeexecutor.Workspace{}, err
	}
	if _, err := codeexecutor.EnsureLayout(root); err != nil {
		return codeexecutor.Workspace{}, err
	}
	for _, dir := range []string{"home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return codeexecutor.Workspace{}, err
		}
	}
	ws := codeexecutor.Workspace{ID: id, Path: root}
	if err := r.materializeManifest(ctx, ws); err != nil {
		return codeexecutor.Workspace{}, err
	}
	if pol.MaxDiskBytes > 0 {
		_ = pol.MaxDiskBytes
	}
	return ws, nil
}

// Cleanup releases workspace resources. Session-persistent workspaces keep files
// by default so later turns in the same session can observe prior file changes.
func (r *Runtime) Cleanup(ctx context.Context, ws codeexecutor.Workspace) error {
	_ = ctx
	if r.sessionPolicy.Persistence == SessionPersistencePerSession {
		return nil
	}
	if ws.Path == "" {
		return nil
	}
	return os.RemoveAll(ws.Path)
}

// Close releases runtime-owned resources such as macOS denial diagnostics
// monitors and permanently disables diagnostics for this runtime. It does not
// remove workspaces; call Cleanup for that. Close is safe to call more than
// once. If shutdown does not complete promptly, Close returns an error and
// retains ownership so a later call can retry.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	return r.closeDenialDiagnostics()
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "default"
	}
	var b strings.Builder
	for _, ch := range id {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '-', ch == '_', ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return shortIDHash(id)
	}
	if len(out) > 128 {
		out = out[:96] + "-" + shortIDHash(id)
	} else if out != id {
		if len(out) > 111 {
			out = out[:111]
		}
		out = out + "-" + shortIDHash(id)
	}
	return out
}

func shortIDHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

func workspacePathForID(root string, id string) (string, string) {
	var parts []string
	for _, part := range strings.FieldsFunc(id, func(ch rune) bool {
		return ch == '/' || ch == '\\'
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, sanitizeID(part))
	}
	if len(parts) == 0 {
		parts = []string{"default"}
	}
	pathParts := append([]string{root, "sandbox"}, parts...)
	return filepath.Join(pathParts...), strings.Join(parts, "_")
}

type workspaceRunLock struct {
	token chan struct{}
	// refs is guarded by Runtime.mu and counts holders plus waiters.
	refs int
}

func newWorkspaceRunLock() *workspaceRunLock {
	lock := &workspaceRunLock{token: make(chan struct{}, 1)}
	lock.token <- struct{}{}
	return lock
}

func (l *workspaceRunLock) LockContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.token:
	}
	if err := ctx.Err(); err != nil {
		l.Unlock()
		return err
	}
	return nil
}

func (l *workspaceRunLock) Unlock() {
	select {
	case l.token <- struct{}{}:
	default:
		panic("sandbox: unlock of unlocked workspace run lock")
	}
}

func workspaceRunLockKey(ws codeexecutor.Workspace) string {
	if ws.Path != "" {
		return ws.Path
	}
	return ws.ID
}

func (r *Runtime) lockWorkspaceRunContext(
	ctx context.Context,
	ws codeexecutor.Workspace,
) (func(), error) {
	key, lock := r.retainWorkspaceRunLock(ws)
	if err := lock.LockContext(ctx); err != nil {
		r.releaseWorkspaceRunLock(key, lock)
		return nil, err
	}
	return func() {
		lock.Unlock()
		r.releaseWorkspaceRunLock(key, lock)
	}, nil
}

func (r *Runtime) retainWorkspaceRunLock(
	ws codeexecutor.Workspace,
) (string, *workspaceRunLock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := workspaceRunLockKey(ws)
	lock := r.runLocks[key]
	if lock == nil {
		lock = newWorkspaceRunLock()
		r.runLocks[key] = lock
	}
	lock.refs++
	return key, lock
}

func (r *Runtime) releaseWorkspaceRunLock(
	key string,
	lock *workspaceRunLock,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lock.refs <= 0 {
		panic("sandbox: release of unretained workspace run lock")
	}
	lock.refs--
	if lock.refs == 0 && r.runLocks[key] == lock {
		delete(r.runLocks, key)
	}
}

func (r *Runtime) applyManifestPolicy() {
	if len(r.manifest.Environment) > 0 {
		if r.envPolicy.Set == nil {
			r.envPolicy.Set = map[string]string{}
		}
		for k, v := range r.manifest.Environment {
			if k != "" {
				r.envPolicy.Set[k] = v
			}
		}
	}
	r.profile = r.profile.WithReadPaths(r.manifest.ExtraReadPaths...)
	r.profile = r.profile.WithWritePaths(r.manifest.ExtraWritePaths...)
}

func (r *Runtime) materializeManifest(ctx context.Context, ws codeexecutor.Workspace) error {
	_ = ctx
	if len(r.manifest.Files) == 0 && len(r.manifest.EphemeralPaths) == 0 {
		return nil
	}
	for _, p := range r.manifest.EphemeralPaths {
		abs, rel, err := r.resolveWorkspacePath(ws, p)
		if err != nil {
			return err
		}
		if isProtectedRel(rel, r.profile.fileSystem.ProtectedMetadata) {
			return deniedf(ErrPathDenied, "manifest", rel, "protected metadata path")
		}
		if err := os.RemoveAll(abs); err != nil {
			return err
		}
	}
	for _, f := range r.manifest.Files {
		abs, rel, err := r.resolveWorkspacePath(ws, f.Path)
		if err != nil {
			return err
		}
		if isProtectedRel(rel, r.profile.fileSystem.ProtectedMetadata) {
			return deniedf(ErrPathDenied, "manifest", rel, "protected metadata path")
		}
		if _, err := os.Stat(abs); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = codeexecutor.DefaultScriptFileMode
		}
		if err := os.WriteFile(abs, f.Content, mode); err != nil {
			return err
		}
		if err := os.Chmod(abs, mode); err != nil {
			return err
		}
	}
	return nil
}
