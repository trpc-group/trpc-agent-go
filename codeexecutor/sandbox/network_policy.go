//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// NetworkMode describes network access.
type NetworkMode string

const (
	// NetworkRestricted blocks outbound networking when the backend can enforce
	// it. On Linux managed profiles this isolates the network namespace and
	// installs a seccomp filter that denies creating new AF_UNIX sockets
	// (including host, guest-local pathname, and abstract sockets) and blocks
	// io_uring syscalls that could bypass that rule. Anonymous AF_UNIX stream and
	// seqpacket socketpairs remain available for local IPC, while datagram
	// socketpairs are denied because their endpoints can reconnect to pathname
	// sockets. Callers that need any pathname/abstract Unix socket must use
	// NetworkEnabled. Linux does not provide a path-level Unix socket allowlist.
	NetworkRestricted NetworkMode = "restricted"
	// NetworkEnabled allows the command to use the host network.
	NetworkEnabled NetworkMode = "enabled"
)

// NetworkPolicy describes network access for a profile.
type NetworkPolicy struct {
	Mode NetworkMode
}

func validateNetworkPolicy(policy NetworkPolicy) error {
	switch policy.Mode {
	case NetworkRestricted, NetworkEnabled:
		return nil
	default:
		return deniedf(
			ErrPolicyViolation,
			"network-mode",
			"",
			"unsupported network mode %q",
			policy.Mode,
		)
	}
}

func validateProfileNetworkPolicy(profile PermissionProfile) error {
	if err := validateNetworkPolicy(profile.network); err != nil {
		return err
	}
	if profile.enforcement() == enforcementDisabled &&
		profile.network.Mode != NetworkEnabled {
		return deniedf(
			ErrPolicyViolation,
			"network-mode",
			"",
			"disabled sandbox requires explicit network mode %q",
			NetworkEnabled,
		)
	}
	return nil
}
