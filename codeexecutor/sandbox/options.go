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
	"os"
	"path/filepath"
	"time"
)

const (
	defaultOutputMaxBytes = 1 << 20
	defaultRunTimeout     = 30 * time.Second
)

// Option configures the sandbox executor.
type Option func(*Runtime)

// WithBackend selects the sandbox backend.
func WithBackend(backend BackendType) Option {
	return func(r *Runtime) {
		if backend != "" {
			r.backend = backend
		}
	}
}

// WithPermissionProfile sets the complete permission profile.
func WithPermissionProfile(profile PermissionProfile) Option {
	return func(r *Runtime) {
		r.profile = normalizeProfile(profile)
	}
}

// WithSessionPolicy sets the session lifecycle policy.
func WithSessionPolicy(policy SessionPolicy) Option {
	return func(r *Runtime) {
		r.sessionPolicy = normalizeSessionPolicy(policy)
	}
}

// WithShellEnvironmentPolicy sets shell environment inheritance and overrides.
func WithShellEnvironmentPolicy(policy ShellEnvironmentPolicy) Option {
	return func(r *Runtime) {
		r.envPolicy = normalizeShellEnvironmentPolicy(policy)
	}
}

// WithWorkspaceRoot sets the directory that contains sandbox sessions.
func WithWorkspaceRoot(root string) Option {
	return func(r *Runtime) {
		if root != "" {
			r.root = root
		}
	}
}

// WithOutputMaxBytes limits stdout/stderr capture per stream.
func WithOutputMaxBytes(n int) Option {
	return func(r *Runtime) {
		if n > 0 {
			r.outputMaxBytes = n
		}
	}
}

// WithDefaultTimeout sets the run timeout used when RunProgramSpec.Timeout is
// empty.
func WithDefaultTimeout(timeout time.Duration) Option {
	return func(r *Runtime) {
		if timeout > 0 {
			r.defaultTimeout = timeout
		}
	}
}

// WithManifest sets the initial sandbox session manifest. The manifest is
// applied when a workspace is created or reopened.
func WithManifest(manifest Manifest) Option {
	return func(r *Runtime) {
		r.manifest = manifest
	}
}

func defaultWorkspaceRoot() string {
	return filepath.Join(os.TempDir(), "trpc-agent-go-sandbox")
}

func normalizeProfile(profile PermissionProfile) PermissionProfile {
	if profile.typ == "" {
		profile.typ = profileManaged
	}
	if profile.network.Mode == "" {
		profile.network.Mode = NetworkRestricted
	}
	if profile.typ == profileDisabled {
		profile.network.Mode = NetworkEnabled
	}
	if profile.fileSystem.ProtectedMetadata == nil {
		profile.fileSystem.ProtectedMetadata = defaultProtectedMetadata()
	}
	return profile
}

func normalizeSessionPolicy(policy SessionPolicy) SessionPolicy {
	if !policy.PersistFilesAcrossTurns && !policy.MutatingCommandsSerial {
		return SessionPolicy{
			PersistFilesAcrossTurns: true,
			MutatingCommandsSerial:  true,
		}
	}
	return policy
}

func normalizeShellEnvironmentPolicy(policy ShellEnvironmentPolicy) ShellEnvironmentPolicy {
	if policy.Inherit == "" {
		policy.Inherit = ShellEnvironmentPolicyInheritAll
	}
	if policy.Set == nil {
		policy.Set = map[string]string{}
	}
	return policy
}
