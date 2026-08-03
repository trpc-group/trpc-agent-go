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
	// it. Linux v1 uses an isolated network namespace for AF_INET/AF_INET6, but
	// does not isolate host AF_UNIX sockets visible through filesystem mounts.
	NetworkRestricted NetworkMode = "restricted"
	// NetworkEnabled allows the command to use the host network.
	NetworkEnabled NetworkMode = "enabled"
	// NetworkControlled keeps host IP networking isolated and allows egress
	// only through a host-managed controlled proxy (Linux: UDS + loopback
	// relay + HTTP_PROXY). It controls HTTP/HTTPS IP egress; it does not make
	// the proxy the sole host communication channel because visible host
	// AF_UNIX sockets remain reachable. Describe().NetworkAllowed remains false.
	NetworkControlled NetworkMode = "controlled"
)

// NetworkPolicy describes network access for a profile.
//
// Compatibility: do not add fields to this struct; callers may use unkeyed
// literals. Controlled egress endpoint configuration uses
// PermissionProfile.WithControlledEgressProxy instead.
type NetworkPolicy struct {
	Mode NetworkMode
}

// ControlledEgressProxy configures the host controlled-egress proxy endpoint.
type ControlledEgressProxy struct {
	// UnixPath is the absolute path of the host AF_UNIX HTTP proxy socket.
	// On Linux the runtime rejects paths inside guest-writable mounts.
	UnixPath string
	// RelayPort is the guest loopback port for HTTP_PROXY (default 17923).
	RelayPort int
}

// resolvedNetworkPolicy is the validated backend-facing network configuration.
type resolvedNetworkPolicy struct {
	mode           NetworkMode
	isolateNetwork bool
	unixPath       string
	relayPort      int
}

// resolveNetworkPolicy normalizes and validates a profile before a backend
// renders it. Empty modes become restricted; unknown and incomplete modes fail
// closed.
func resolveNetworkPolicy(
	profile PermissionProfile,
) (resolvedNetworkPolicy, error) {
	mode := profile.network.Mode
	if mode == "" {
		mode = NetworkRestricted
	}
	switch mode {
	case NetworkRestricted:
		return resolvedNetworkPolicy{
			mode:           NetworkRestricted,
			isolateNetwork: true,
		}, nil
	case NetworkEnabled:
		return resolvedNetworkPolicy{
			mode:           NetworkEnabled,
			isolateNetwork: false,
		}, nil
	case NetworkControlled:
		if err := profile.controlledEgress.validate(); err != nil {
			return resolvedNetworkPolicy{}, err
		}
		return resolvedNetworkPolicy{
			mode:           NetworkControlled,
			isolateNetwork: true,
			unixPath:       profile.controlledEgress.UnixPath,
			relayPort:      profile.controlledEgress.effectiveRelayPort(),
		}, nil
	default:
		return resolvedNetworkPolicy{}, deniedf(
			ErrPolicyViolation,
			"network",
			"",
			"unsupported network mode %q: want restricted|enabled|controlled",
			mode,
		)
	}
}

func validateNetworkPolicy(profile PermissionProfile) error {
	_, err := resolveNetworkPolicy(profile)
	return err
}
