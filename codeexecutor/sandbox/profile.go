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

// enforcement is the internal execution mode derived from PermissionProfile.
type enforcement string

const (
	// enforcementManaged means trpc-agent-go must enforce an OS sandbox.
	enforcementManaged enforcement = "managed"
	// enforcementDisabled means no sandbox is requested.
	enforcementDisabled enforcement = "disabled"
	// enforcementExternal means isolation is supplied by an external system.
	enforcementExternal enforcement = "external"
)

// permissionProfileType selects how sandbox permissions are enforced.
type permissionProfileType string

const (
	// profileManaged uses the sandbox backend selected by this package.
	profileManaged permissionProfileType = "managed"
	// profileDisabled intentionally disables sandboxing.
	profileDisabled permissionProfileType = "disabled"
	// profileExternal declares that an outside system is already enforcing
	// isolation. This executor does not claim OS enforcement in that mode.
	profileExternal permissionProfileType = "external"
)

// PermissionProfile is the public sandbox permission model. It intentionally
// owns both filesystem and network policy so callers cannot request contradictory
// combinations such as read-only + disabled enforcement.
type PermissionProfile struct {
	typ        permissionProfileType
	fileSystem fileSystemPolicy
	network    NetworkPolicy
	macOS      macOSProfilePolicy
}

// macOSProfilePolicy describes macOS Seatbelt-specific controls. It is kept off
// the public NetworkPolicy struct so the cross-platform network model stays
// binary and existing NetworkPolicy literals remain source-compatible.
type macOSProfilePolicy struct {
	allowSystemTrustServices bool
	unixSocketPaths          []string
}

// enforcement derives the execution mode from the profile.
func (p PermissionProfile) enforcement() enforcement {
	switch p.typ {
	case profileDisabled:
		return enforcementDisabled
	case profileExternal:
		return enforcementExternal
	default:
		return enforcementManaged
	}
}

// ReadOnlyProfile returns a managed profile with read-only host visibility and
// restricted networking.
func ReadOnlyProfile() PermissionProfile {
	return PermissionProfile{
		typ: profileManaged,
		fileSystem: fileSystemPolicy{
			Rules: []fileSystemRule{{
				Kind:    ruleSpecial,
				Access:  accessRead,
				Special: specialRoot,
			}},
			ProtectedMetadata: defaultProtectedMetadata(),
		},
		network: NetworkPolicy{Mode: NetworkRestricted},
	}
}

// WorkspaceWriteProfile returns the default managed profile: read-only host
// root, writable session workspace, protected metadata, restricted networking.
func WorkspaceWriteProfile() PermissionProfile {
	p := ReadOnlyProfile()
	p.fileSystem.Rules = append(p.fileSystem.Rules,
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialWorkspace},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialWork},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialHome},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialTmp},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialRuns},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialOut},
		fileSystemRule{Kind: ruleSpecial, Access: accessWrite, Special: specialSkills},
	)
	return p
}

// DangerFullAccessProfile intentionally disables sandboxing.
func DangerFullAccessProfile() PermissionProfile {
	return PermissionProfile{
		typ:     profileDisabled,
		network: NetworkPolicy{Mode: NetworkEnabled},
	}
}

// ExternalSandboxProfile declares that an outside system already enforces
// sandboxing. The executor will not silently run this as local execution.
func ExternalSandboxProfile(network NetworkPolicy) PermissionProfile {
	if network.Mode == "" {
		network.Mode = NetworkRestricted
	}
	return PermissionProfile{typ: profileExternal, network: network}
}

// WithNetworkPolicy sets network access for the profile.
func (p PermissionProfile) WithNetworkPolicy(policy NetworkPolicy) PermissionProfile {
	if policy.Mode == "" {
		policy.Mode = NetworkRestricted
	}
	p.network = policy
	return p
}

// WithMacOSWeakerNetworkIsolation allows macOS system trust services such as
// com.apple.trustd.agent inside the Seatbelt sandbox. It is useful for Go-based
// CLI tools that validate TLS certificates through custom CAs, but weakens
// network isolation and has no effect on non-macOS backends.
func (p PermissionProfile) WithMacOSWeakerNetworkIsolation() PermissionProfile {
	p.macOS.allowSystemTrustServices = true
	return p
}

// WithMacOSUnixSocketPaths allows macOS Seatbelt access to exact AF_UNIX socket
// paths. Linux keeps the existing namespace-level network model and does not
// claim support for these macOS-specific paths.
func (p PermissionProfile) WithMacOSUnixSocketPaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p.macOS.unixSocketPaths = append(p.macOS.unixSocketPaths, path)
	}
	return p
}

// WithReadPaths adds read grants.
func (p PermissionProfile) WithReadPaths(paths ...string) PermissionProfile {
	for _, path := range paths {
		if path == "" {
			continue
		}
		p = p.withFileSystemRule(fileSystemRule{
			Kind: rulePath, Access: accessRead, Path: path,
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
		p = p.withFileSystemRule(fileSystemRule{
			Kind: rulePath, Access: accessWrite, Path: path,
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
		p = p.withFileSystemRule(fileSystemRule{
			Kind: rulePath, Access: accessNone, Path: path,
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
		p = p.withFileSystemRule(fileSystemRule{
			Kind: ruleGlob, Access: accessNone, Glob: pattern,
		})
	}
	return p
}

func (p PermissionProfile) withFileSystemRule(rule fileSystemRule) PermissionProfile {
	rules := make([]fileSystemRule, 0, len(p.fileSystem.Rules)+1)
	rules = append(rules, p.fileSystem.Rules...)
	rules = append(rules, rule)
	p.fileSystem.Rules = rules
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
	add, _ := ctx.Value(additionalPermissionsKey{}).(AdditionalPermissions)
	return add
}

func applyAdditionalPermissions(p PermissionProfile, add AdditionalPermissions) PermissionProfile {
	p = p.WithReadPaths(add.ReadPaths...)
	p = p.WithWritePaths(add.WritePaths...)
	if add.Network != nil {
		p = p.WithNetworkPolicy(*add.Network)
	}
	return p
}
