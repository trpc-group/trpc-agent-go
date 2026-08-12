# Sandbox Network Policy

Sandbox profiles own network policy through `NetworkPolicy.Mode`, configured on
a profile with `WithNetworkPolicy`. The policy is a binary switch:
`NetworkRestricted` or `NetworkEnabled`. Managed profiles default to
`NetworkRestricted`, so code runs without host network access unless the caller
explicitly selects `NetworkEnabled`.

## Policy Model

- `NetworkRestricted` is the safe default for managed execution. The runtime
  reports `NetworkAllowed=false` and asks the backend to block outbound
  networking when the backend can enforce it.
- `NetworkEnabled` allows the command to use the host network. On Linux this
  means the command is launched without network namespace isolation and without
  the AF_UNIX seccomp filter described below.

Profile enforcement is separate from network policy. `DangerFullAccessProfile()`
intentionally runs without local sandbox enforcement and is normalized to
`NetworkEnabled`; `ExternalSandboxProfile` declares that another system is
responsible for enforcing the requested policy.

## Linux Enforcement

The Linux backend uses `bubblewrap` as the local enforcement boundary. For
`NetworkRestricted`, the runtime appends `--unshare-net` to the `bwrap` command
line before launching the user process. This creates a fresh network namespace
for the sandboxed process, so it cannot use the host network stack or host
interfaces.

`NetworkRestricted` also loads a classic BPF seccomp filter through
`bwrap --seccomp FD`. The filter:

- returns `EPERM` for `socket(AF_UNIX, ...)` / `socket(AF_LOCAL, ...)`;
- returns `EPERM` for `socketpair(AF_UNIX, SOCK_DGRAM, ...)`, including socket
  type flags, because datagram socketpair endpoints can reconnect to pathname
  sockets;
- returns `EPERM` for `io_uring_setup`, `io_uring_enter`, and
  `io_uring_register`, which otherwise could create sockets without `socket(2)`;
- leaves `socketpair(AF_UNIX, SOCK_STREAM, ...)` and
  `socketpair(AF_UNIX, SOCK_SEQPACKET, ...)` available for anonymous local IPC;
- does not provide a path-level Unix socket allowlist.

Because the managed Linux profile still uses `--ro-bind / /`, host socket paths
such as `docker.sock` remain visible in the mount namespace. The seccomp filter
is what makes them unusable: the guest cannot create a new AF_UNIX file
descriptor to `connect(2)` them. The same rule also blocks guest-local pathname
and abstract Unix sockets. Callers that need pathname or abstract Unix socket IPC must
choose `NetworkEnabled` (including through temporary `WithAdditionalPermissions`);
anonymous stream and seqpacket socketpairs remain available in `NetworkRestricted`.

Restricted Linux runs fail closed when:

- the GOARCH is not `amd64` or `arm64`;
- the kernel is older than 4.8 (historical seccomp/ptrace bypass);
- bubblewrap or the kernel cannot load the filter.

On Linux 4.8–4.13, seccomp may degrade `SECCOMP_RET_KILL_PROCESS` to
kill-thread for the wrong-architecture and amd64 x32 reject paths. The denied
syscall still does not execute, and the AF_UNIX / `io_uring` `EPERM` returns are
unchanged. The minimum kernel remains 4.8; this is a compatibility note, not a
fail-open path.

When `NetworkEnabled` is selected, the backend omits `--unshare-net` and the
seccomp filter. The command then shares the host network namespace and may use
visible host Unix sockets while still using the rest of the configured sandbox
controls, such as user, PID, mount, environment, and filesystem policy.

## macOS Enforcement

The macOS backend keeps the same public binary model but projects it to
Seatbelt rules instead of a network namespace:

- `NetworkRestricted` does not add broad network allow rules.
- `NetworkEnabled` adds broad `network-outbound`, `network-inbound`, and system
  socket allowances, plus the system services needed for ordinary host network
  use.

macOS has network-adjacent IPC surfaces that Linux namespaces do not model in
the same way. `WithMacOSWeakerNetworkIsolation` explicitly allows system trust
services such as `com.apple.trustd.agent`, which can help Go-based CLI tools
validate TLS certificates through custom CAs but weakens isolation.
`WithMacOSUnixSocketPaths` allows AF_UNIX socket bind/connect operations for
exact absolute socket paths. These are macOS backend extensions; the Linux
backend does not claim support for equivalent path-level Unix socket policy.

## Scope

This design keeps the first Linux implementation intentionally binary:
networking is either isolated or inherited from the host. It does not currently
implement per-domain, per-IP, or per-port allow lists. If finer-grained egress
control is needed later, it should be layered outside this backend or added as a
new backend capability with explicit policy fields rather than overloading
`NetworkRestricted`.
