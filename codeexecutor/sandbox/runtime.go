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

	mu       sync.Mutex
	runLocks map[string]*sync.Mutex

	preflightOnce  sync.Once
	preflightErr   error
	bwrapPath      string
	bwrapMountProc bool
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
		runLocks:       map[string]*sync.Mutex{},
	}
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
		Isolation:      isolation,
		NetworkAllowed: profile.network.Mode == NetworkEnabled,
		ReadOnlyMount:  profile.enforcement() == enforcementManaged,
		Streaming:      false,
	}
}

// CreateWorkspace creates or opens the deterministic directory for an
// execution/session id.
func (r *Runtime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_ = ctx
	if execID == "" {
		execID = "default"
	}
	root, id := workspacePathForID(r.root, execID)
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

func (r *Runtime) runLock(ws codeexecutor.Workspace) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ws.Path
	if key == "" {
		key = ws.ID
	}
	if l := r.runLocks[key]; l != nil {
		return l
	}
	l := &sync.Mutex{}
	r.runLocks[key] = l
	return l
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
