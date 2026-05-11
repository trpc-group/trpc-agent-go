# Sandbox Backend Support

The sandbox runtime provides a managed OS sandbox only where the package has a
native backend implementation. Other profiles may still be selected by callers,
but they do not provide local managed sandbox enforcement on every platform.

| Platform | Managed OS sandbox support | Backend | Notes |
| --- | --- | --- | --- |
| Linux | Supported | `linux-bubblewrap` | Uses `bubblewrap` with user, PID, mount, and optional network namespaces. |
| macOS | Not implemented | N/A | Managed profiles return an unsupported-backend error. Disabled profiles run without sandboxing. |
| Windows | Not implemented | N/A | Managed profiles return an unsupported-backend error. Disabled profiles run without sandboxing. |

## Prerequisites

On Linux, Sandbox Code Executor uses the `bwrap` executable found on `PATH`. If `bwrap` is unavailable, setup fails before the command starts.

## Network

Sandbox profiles own network policy through `NetworkPolicy.Mode`. The policy is
a binary switch: `NetworkRestricted` or `NetworkEnabled`. Managed profiles
default to `NetworkRestricted`, so code runs without host network access unless
the caller explicitly selects `NetworkEnabled`.

### Policy model

- `NetworkRestricted` is the safe default for managed execution. The runtime
  reports `NetworkAllowed=false` and asks the backend to block outbound
  networking when the backend can enforce it.
- `NetworkEnabled` allows the command to use the host network. On Linux this
  means the command is launched without network namespace isolation.

`ProfileDisabled` and `ProfileExternal` are profile enforcement modes, not
network policy modes. A disabled profile intentionally runs without local
sandbox enforcement and is normalized to `NetworkEnabled`; an external profile
declares that another system is responsible for enforcing the requested policy.

### Linux implementation

The Linux backend uses `bubblewrap` as the local enforcement boundary. For
`NetworkRestricted`, the runtime appends `--unshare-net` to the `bwrap` command
line before launching the user process. This creates a fresh network namespace
for the sandboxed process, so it cannot use the host network stack or host
interfaces.

When `NetworkEnabled` is selected, the backend simply omits `--unshare-net`.
The command then shares the host network namespace while still using the rest of
the configured sandbox controls, such as user, PID, mount, environment, and
filesystem policy.

This design keeps the first Linux implementation intentionally binary:
networking is either isolated or inherited from the host. It does not currently
implement per-domain, per-IP, or per-port allow lists. If finer-grained egress
control is needed later, it should be layered outside this backend or added as a
new backend capability with explicit policy fields rather than overloading
`NetworkRestricted`.

## File System
