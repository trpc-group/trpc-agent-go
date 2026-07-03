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
  means the command is launched without network namespace isolation.

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

When `NetworkEnabled` is selected, the backend simply omits `--unshare-net`.
The command then shares the host network namespace while still using the rest of
the configured sandbox controls, such as user, PID, mount, environment, and
filesystem policy.

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
