//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandbox provides an OS-level sandbox code executor.
package sandbox

import (
	"context"
	"time"
)

// Enforcement is the internal execution mode derived from PermissionProfile.
type Enforcement string

const (
	// EnforcementManaged means trpc-agent-go must enforce an OS sandbox.
	EnforcementManaged Enforcement = "managed"
	// EnforcementDisabled means no sandbox is requested.
	EnforcementDisabled Enforcement = "disabled"
	// EnforcementExternal means isolation is supplied by an external system.
	EnforcementExternal Enforcement = "external"
)

// PermissionProfileType selects how sandbox permissions are enforced.
type PermissionProfileType string

const (
	// ProfileManaged uses the sandbox backend selected by this package.
	ProfileManaged PermissionProfileType = "managed"
	// ProfileDisabled intentionally disables sandboxing.
	ProfileDisabled PermissionProfileType = "disabled"
	// ProfileExternal declares that an outside system is already enforcing
	// isolation. This executor does not claim OS enforcement in that mode.
	ProfileExternal PermissionProfileType = "external"
)

// NetworkMode describes network access.
type NetworkMode string

const (
	// NetworkRestricted blocks outbound networking when the backend can enforce
	// it. Linux v1 uses an isolated network namespace.
	NetworkRestricted NetworkMode = "restricted"
	// NetworkEnabled allows the command to use the host network.
	NetworkEnabled NetworkMode = "enabled"
)

// NetworkPolicy describes network access for a profile.
type NetworkPolicy struct {
	Mode NetworkMode
}

// FileSystemAccess describes a filesystem rule's grant or denial.
type FileSystemAccess string

const (
	// AccessRead grants read-only access.
	AccessRead FileSystemAccess = "read"
	// AccessWrite grants read and write access.
	AccessWrite FileSystemAccess = "write"
	// AccessDenyRead denies reads and takes precedence over read/write grants.
	AccessDenyRead FileSystemAccess = "deny-read"
	// AccessDenyReadGlob denies reads for glob matches.
	AccessDenyReadGlob FileSystemAccess = "deny-read-glob"
)

// FileSystemRuleKind describes how a filesystem rule target is interpreted.
type FileSystemRuleKind string

const (
	// RulePath targets a concrete path. Relative paths are workspace-relative;
	// absolute paths are host paths.
	RulePath FileSystemRuleKind = "path"
	// RuleSpecial targets a well-known sandbox path.
	RuleSpecial FileSystemRuleKind = "special"
	// RuleGlob targets a workspace-relative glob.
	RuleGlob FileSystemRuleKind = "glob"
)

// SpecialPath identifies well-known session-scoped directories.
type SpecialPath string

const (
	SpecialRoot      SpecialPath = "root"
	SpecialWorkspace SpecialPath = "workspace"
	SpecialWork      SpecialPath = "work"
	SpecialHome      SpecialPath = "home"
	SpecialTmp       SpecialPath = "tmp"
	SpecialRuns      SpecialPath = "runs"
	SpecialOut       SpecialPath = "out"
	SpecialSkills    SpecialPath = "skills"
)

// FileSystemRule declares one filesystem grant or denial.
type FileSystemRule struct {
	Kind    FileSystemRuleKind
	Access  FileSystemAccess
	Path    string
	Special SpecialPath
	Glob    string
}

// FileSystemPolicy is the filesystem portion of a PermissionProfile.
type FileSystemPolicy struct {
	Rules             []FileSystemRule
	ProtectedMetadata []string
}

// PermissionProfile is the public sandbox permission model. It intentionally
// owns both filesystem and network policy so callers cannot request contradictory
// combinations such as read-only + disabled enforcement.
type PermissionProfile struct {
	Type       PermissionProfileType
	FileSystem FileSystemPolicy
	Network    NetworkPolicy
}

// Enforcement derives the execution mode from the profile.
func (p PermissionProfile) Enforcement() Enforcement {
	switch p.Type {
	case ProfileDisabled:
		return EnforcementDisabled
	case ProfileExternal:
		return EnforcementExternal
	default:
		return EnforcementManaged
	}
}

// ReadOnlyProfile returns a managed profile with read-only host visibility and
// restricted networking.
func ReadOnlyProfile() PermissionProfile {
	return PermissionProfile{
		Type: ProfileManaged,
		FileSystem: FileSystemPolicy{
			Rules: []FileSystemRule{{
				Kind:    RuleSpecial,
				Access:  AccessRead,
				Special: SpecialRoot,
			}},
			ProtectedMetadata: defaultProtectedMetadata(),
		},
		Network: NetworkPolicy{Mode: NetworkRestricted},
	}
}

// WorkspaceWriteProfile returns the default managed profile: read-only host
// root, writable session workspace, protected metadata, restricted networking.
func WorkspaceWriteProfile() PermissionProfile {
	p := ReadOnlyProfile()
	p.FileSystem.Rules = append(p.FileSystem.Rules,
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialWorkspace},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialWork},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialHome},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialTmp},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialRuns},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialOut},
		FileSystemRule{Kind: RuleSpecial, Access: AccessWrite, Special: SpecialSkills},
	)
	return p
}

// DangerFullAccessProfile intentionally disables sandboxing.
func DangerFullAccessProfile() PermissionProfile {
	return PermissionProfile{
		Type:    ProfileDisabled,
		Network: NetworkPolicy{Mode: NetworkEnabled},
	}
}

// ExternalSandboxProfile declares that an outside system already enforces
// sandboxing. The executor will not silently run this as local execution.
func ExternalSandboxProfile(network NetworkPolicy) PermissionProfile {
	if network.Mode == "" {
		network.Mode = NetworkRestricted
	}
	return PermissionProfile{Type: ProfileExternal, Network: network}
}

// WithReadPaths adds read grants.
func (p PermissionProfile) WithReadPaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p.FileSystem.Rules = append(p.FileSystem.Rules, FileSystemRule{
			Kind: RulePath, Access: AccessRead, Path: path,
		})
	}
	return p
}

// WithWritePaths adds write grants.
func (p PermissionProfile) WithWritePaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p.FileSystem.Rules = append(p.FileSystem.Rules, FileSystemRule{
			Kind: RulePath, Access: AccessWrite, Path: path,
		})
	}
	return p
}

// WithDenyReadPaths adds concrete deny-read rules.
func (p PermissionProfile) WithDenyReadPaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p.FileSystem.Rules = append(p.FileSystem.Rules, FileSystemRule{
			Kind: RulePath, Access: AccessDenyRead, Path: path,
		})
	}
	return p
}

// WithDenyReadGlobs adds workspace-relative deny-read glob rules.
func (p PermissionProfile) WithDenyReadGlobs(patterns ...string) PermissionProfile {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		p.FileSystem.Rules = append(p.FileSystem.Rules, FileSystemRule{
			Kind: RuleGlob, Access: AccessDenyReadGlob, Glob: pattern,
		})
	}
	return p
}

// AdditionalPermissions are temporary per-command grants.
type AdditionalPermissions struct {
	ReadPaths  []string
	WritePaths []string
	Network    *NetworkPolicy
}

type additionalPermissionsKey struct{}

// WithAdditionalPermissions attaches temporary per-command grants to ctx.
func WithAdditionalPermissions(ctx context.Context, add AdditionalPermissions) context.Context {
	return context.WithValue(ctx, additionalPermissionsKey{}, add)
}

func additionalPermissionsFromContext(ctx context.Context) AdditionalPermissions {
	if ctx == nil {
		return AdditionalPermissions{}
	}
	add, _ := ctx.Value(additionalPermissionsKey{}).(AdditionalPermissions)
	return add
}

func applyAdditionalPermissions(p PermissionProfile, add AdditionalPermissions) PermissionProfile {
	p = p.WithReadPaths(add.ReadPaths...)
	p = p.WithWritePaths(add.WritePaths...)
	if add.Network != nil {
		p.Network = *add.Network
	}
	return p
}

// SessionPolicy controls sandbox session lifecycle.
type SessionPolicy struct {
	PersistFilesAcrossTurns bool
	MutatingCommandsSerial  bool
}

// EnvironmentInheritMode controls which host environment variables are visible.
type EnvironmentInheritMode string

const (
	// EnvInheritNone starts with an empty environment.
	EnvInheritNone EnvironmentInheritMode = "none"
	// EnvInheritCore includes only safe core variables such as PATH and locale.
	EnvInheritCore EnvironmentInheritMode = "core"
	// EnvInheritAll includes the host environment, then applies exclude/set.
	EnvInheritAll EnvironmentInheritMode = "all"
)

// EnvironmentPolicy controls environment inheritance and overrides.
type EnvironmentPolicy struct {
	Inherit     EnvironmentInheritMode
	IncludeOnly []string
	Exclude     []string
	Set         map[string]string
}

// BackendType selects the OS sandbox backend.
type BackendType string

const (
	// BackendAuto selects the native backend for the current platform.
	BackendAuto BackendType = "auto"
	// BackendLinuxBubblewrap uses bubblewrap on Linux.
	BackendLinuxBubblewrap BackendType = "linux-bubblewrap"
)

// BackendCapabilities reports backend support above the generic engine
// capabilities exposed by codeexecutor.Engine.
type BackendCapabilities struct {
	OSSandbox          bool
	PTY                bool
	Stdin              bool
	NetworkIsolation   bool
	DenyReadGlob       bool
	Snapshot           bool
	Ports              bool
	ExternalPathGrants bool
	ProtectedPathMasks bool
	PerCommandGrants   bool
}

// AuditRecord is a small execution audit payload suitable for structured logs.
// It deliberately excludes secrets and full environment values.
type AuditRecord struct {
	Backend     BackendType
	SandboxType Enforcement
	SessionID   string
	PolicyID    string
	Cwd         string
	ExitCode    int
	Duration    time.Duration
	TimedOut    bool
	StdoutCut   bool
	StderrCut   bool
}

// Manifest describes the initial sandbox session state v1 can materialize.
// It is intentionally append-only during CreateWorkspace: existing files are
// left in place so live sessions are not silently rewritten.
type Manifest struct {
	Files           []ManifestFile
	Environment     map[string]string
	ExtraReadPaths  []string
	ExtraWritePaths []string
	EphemeralPaths  []string
}

// ManifestFile is a workspace-relative file in a sandbox manifest.
type ManifestFile struct {
	Path    string
	Content []byte
	Mode    uint32
}
