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

// ReadMode selects the fallback for filesystem reads when no path rule
// determines access. Under managed enforcement, path rules and mandatory
// protections apply in both modes. The zero value selects ReadModeGranted.
// Disabled enforcement bypasses access restrictions; external enforcement
// remains the external system's responsibility.
type ReadMode string

const (
	// ReadModeGranted permits reads only through effective filesystem grants.
	// Write grants also permit reads. Built-in profiles grant platform runtime
	// resources and their workspace explicitly.
	ReadModeGranted ReadMode = "granted"
	// ReadModeHost permits reads that the host process could perform when no
	// path rule determines access. It does not provide confidentiality for
	// arbitrary host files. Credential and session protections still apply.
	ReadModeHost ReadMode = "host"
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

// ReadOnlyProfile returns a managed profile granting read-only access to
// platform runtime resources and the session workspace, with restricted
// networking. Its read mode is ReadModeGranted. On Linux restricted
// networking denies pathname and abstract AF_UNIX sockets and AF_VSOCK;
// anonymous stream and seqpacket socketpairs remain available.
func ReadOnlyProfile() PermissionProfile {
	return PermissionProfile{
		typ: profileManaged,
		fileSystem: fileSystemPolicy{
			ReadMode: ReadModeGranted,
			Rules: append(platformReadRules(), fileSystemRule{
				Kind: ruleSpecial, Access: accessRead, Special: specialRoot,
			}),
			ProtectedMetadata: defaultProtectedMetadata(),
		},
		network: NetworkPolicy{Mode: NetworkRestricted},
	}
}

// WorkspaceWriteProfile returns the default managed profile: platform runtime
// resources are readable, the session workspace is writable, metadata is
// protected, and networking is restricted. Its read mode is ReadModeGranted.
// Use WithReadMode(ReadModeHost) to opt into broader host reads.
// Files inside the workspace, runtime grants, and explicit grants are readable
// unless restricted by path rules or built-in protections. Arbitrary secret
// filenames are not denied automatically. Host environment inheritance is
// configured separately with WithShellEnvironmentPolicy.
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

func (p PermissionProfile) effectiveReadMode() ReadMode {
	if p.fileSystem.ReadMode == "" {
		return ReadModeGranted
	}
	return p.fileSystem.ReadMode
}

func (p PermissionProfile) exposesHostRoot() bool {
	return p.effectiveReadMode() == ReadModeHost
}

func platformReadRules() []fileSystemRule {
	var rules []fileSystemRule
	for _, path := range platformReadPaths() {
		rules = append(rules, fileSystemRule{
			Kind: rulePath, Access: accessRead, Path: path, optional: true,
		})
	}
	return rules
}

func validateReadMode(p PermissionProfile) error {
	switch p.effectiveReadMode() {
	case ReadModeGranted, ReadModeHost:
		return nil
	default:
		return deniedf(ErrPolicyViolation, "read-mode", "", "unknown read mode %q", p.fileSystem.ReadMode)
	}
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

// WithReadMode returns a profile with the filesystem read fallback set on every
// platform. An empty mode selects ReadModeGranted. Unknown values are retained and
// rejected as policy violations by workspace creation, execution, and file
// operations. This intentionally changes the previous Linux read default.
//
// Setting the mode preserves existing path rules; adding path grants preserves
// the mode. In host mode, read grants do not narrow the fallback to an allowlist.
//
// Path rules, credential protection, and session protection apply in both
// modes. Exact credential path grants remain opt-in exceptions; parent grants
// do not remove credential or session protection. Environment inheritance uses
// WithShellEnvironmentPolicy and is unchanged by the mode. Filename-based
// filtering of arbitrary .env, *.pem, and *.key files is not automatic.
func (p PermissionProfile) WithReadMode(mode ReadMode) PermissionProfile {
	p.fileSystem.ReadMode = mode
	return p
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
// paths. Linux NetworkRestricted combines network-namespace isolation with
// AF_UNIX/AF_VSOCK/io_uring seccomp and does not use these macOS-specific path
// grants.
func (p PermissionProfile) WithMacOSUnixSocketPaths(paths ...string) PermissionProfile {
	var filtered []string
	for _, path := range paths {
		if path != "" {
			filtered = append(filtered, path)
		}
	}
	if len(filtered) == 0 {
		return p
	}
	merged := make([]string, len(p.macOS.unixSocketPaths), len(p.macOS.unixSocketPaths)+len(filtered))
	copy(merged, p.macOS.unixSocketPaths)
	p.macOS.unixSocketPaths = append(merged, filtered...)
	return p
}

// WithReadPaths adds read-only path rules. Relative paths are workspace-relative;
// absolute paths refer to host resources. A more-specific read rule can restrict
// a broader write grant. Equally specific no-access and write rules take
// precedence over read rules. The profile's read mode is unchanged.
// Grants authorize resolved resources; parent grants do not authorize external
// targets reached through descendant symlinks.
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

// WithWritePaths adds path rules granting both read and write access. Relative
// paths are workspace-relative; absolute paths refer to host resources.
// More-specific rules and mandatory protections can restrict these grants.
// The profile's read mode is unchanged.
// Grants authorize resolved resources; parent grants do not authorize external
// targets reached through descendant symlinks.
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
