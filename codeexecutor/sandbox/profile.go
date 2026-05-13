//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import "context"

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
		p = p.withFileSystemRule(FileSystemRule{
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
		p = p.withFileSystemRule(FileSystemRule{
			Kind: RulePath, Access: AccessWrite, Path: path,
		})
	}
	return p
}

// WithNoAccessPaths adds concrete no-access rules. Matching paths are neither
// readable nor writable.
func (p PermissionProfile) WithNoAccessPaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p = p.withFileSystemRule(FileSystemRule{
			Kind: RulePath, Access: AccessNone, Path: path,
		})
	}
	return p
}

// WithNoAccessGlobs adds workspace-relative no-access glob rules. Matching
// files are neither readable nor writable.
func (p PermissionProfile) WithNoAccessGlobs(patterns ...string) PermissionProfile {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		p = p.withFileSystemRule(FileSystemRule{
			Kind: RuleGlob, Access: AccessNone, Glob: pattern,
		})
	}
	return p
}

func (p PermissionProfile) withFileSystemRule(rule FileSystemRule) PermissionProfile {
	rules := make([]FileSystemRule, 0, len(p.FileSystem.Rules)+1)
	rules = append(rules, p.FileSystem.Rules...)
	rules = append(rules, rule)
	p.FileSystem.Rules = rules
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
