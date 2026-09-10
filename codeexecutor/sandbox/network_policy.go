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
	// (including host, guest-local pathname, and abstract sockets), denies
	// AF_VSOCK sockets that would otherwise bypass the empty netns via the host
	// vsock transport, and blocks io_uring syscalls that could bypass those
	// rules. Anonymous AF_UNIX stream and seqpacket socketpairs remain available
	// for local IPC, while datagram socketpairs are denied because their
	// endpoints can reconnect to pathname sockets. Callers that need any
	// pathname/abstract Unix socket or AF_VSOCK must use NetworkEnabled. Linux
	// does not provide a path-level Unix socket allowlist.
	NetworkRestricted NetworkMode = "restricted"
	// NetworkEnabled allows the command to use the host network.
	NetworkEnabled NetworkMode = "enabled"
	// NetworkControlled keeps the Linux NetworkRestricted isolation (netns +
	// AF_UNIX/AF_VSOCK/io_uring seccomp) and allows HTTP/HTTPS egress only
	// through a caller-owned host proxy. The guest talks to loopback HTTP_PROXY;
	// a trusted relay connects to the host proxy before the workload-only
	// seccomp filter is applied. Describe().NetworkAllowed remains false.
	//
	// Compatibility: do not add proxy endpoint fields to NetworkPolicy; callers
	// may use unkeyed literals. Configure the host proxy with
	// PermissionProfile.WithControlledEgressProxy.
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
	// On Linux the runtime rejects paths inside guest-writable mounts. The
	// trusted relay dials this path before workload-only seccomp is applied.
	UnixPath string
	// RelayPort is the guest loopback port for HTTP_PROXY (default 17923).
	RelayPort int
}

type resolvedNetworkPolicy struct {
	mode           NetworkMode
	isolateNetwork bool
	unixPath       string
	relayPort      int
}

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

func validateNetworkPolicy(policy NetworkPolicy) error {
	switch policy.Mode {
	case "", NetworkRestricted, NetworkEnabled, NetworkControlled:
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
	if _, err := resolveNetworkPolicy(profile); err != nil {
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
