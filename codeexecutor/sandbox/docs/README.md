# Sandbox Backend Support

The sandbox runtime provides a managed OS sandbox only where the package has a
native backend implementation. Other profiles may still be selected by callers,
but they do not provide local managed sandbox enforcement on every platform.

| Platform | Managed OS sandbox support | Backend | Notes |
| --- | --- | --- | --- |
| Linux | Supported | `linux-bubblewrap` | Uses `bubblewrap` with user, PID, mount, and optional network namespaces. |
| macOS | Supported | `macos-sandbox-exec` | Uses Apple Seatbelt through `/usr/bin/sandbox-exec`. See [`MACOS_BACKEND.md`](MACOS_BACKEND.md) for platform differences. |
| Windows | Not implemented | N/A | Managed profiles return an unsupported-backend error. Disabled profiles run without sandboxing. |

## Prerequisites

On Linux, Sandbox Code Executor uses the `bwrap` executable found on `PATH`. If `bwrap` is unavailable, setup fails before the command starts.

On macOS, managed sandbox profiles use `/usr/bin/sandbox-exec`. Managed
execution fails closed if the tool is unavailable or the host rejects Seatbelt
profiles.

When the runtime itself runs inside Docker, Kubernetes, or a managed container
platform, the outer container must allow the namespace and mount operations
needed by `bwrap`. See
[`DEPLOYMENT_INSIDE_DOCKER.md`](DEPLOYMENT_INSIDE_DOCKER.md) for recommended
permissions, risk notes, and validation commands.

## Network

Network policy is enforced as a binary boundary between isolated and host
network access. Managed profiles use the `restricted` / `enabled` access model
described in [`NETWORK_POLICY.md`](NETWORK_POLICY.md). In short, managed
profiles default to restricted networking unless the caller explicitly enables
host network access:

- `NetworkRestricted` asks the backend to block outbound networking when it can
  enforce that boundary.
- `NetworkEnabled` allows the command to use the host network. On Linux this
  means the command is launched without network namespace isolation. On macOS
  this means the generated Seatbelt profile includes broad network allow rules.

## File System

File system policy is enforced as a boundary between the sandbox workspace and
the host file system. Managed profiles use the `read` / `write` / `none`
access model described in [`FILE_SYSTEM_POLICY.md`](FILE_SYSTEM_POLICY.md). In
short, `write` includes read access, while `none` means neither readable nor
writable:

- `ReadOnlyProfile` grants read access to the sandbox root and keeps networking
  restricted.
- `WorkspaceWriteProfile` is the default managed profile. It starts from
  `ReadOnlyProfile` and grants write access to the session workspace and its
  well-known working directories.
- `WithReadPaths` and `WithWritePaths` add explicit path grants. Relative paths
  are resolved inside the workspace. Absolute paths are treated as host paths
  and must be granted explicitly before they are mounted into the sandbox.
- `WithNoAccessPaths` and `WithNoAccessGlobs` create `none` rules. Matching
  paths are neither readable nor writable.

## Shell Environment

Shell environment policy controls which host environment variables are visible
to sandboxed commands. The inheritance, filtering, override, and runtime
variable injection model is described in
[`SHELL_ENVIRONMENT_POLICY.md`](SHELL_ENVIRONMENT_POLICY.md). In short, callers
can inherit all, core, or no host variables, apply excludes or allow-lists, and
the runtime always injects stable workspace variables such as `HOME`, `TMPDIR`,
`WORKSPACE_DIR`, and `OUTPUT_DIR`.

For backward compatibility, `RunProgram` keeps its existing environment
behavior and does not advertise generic `CleanEnv` support. The new
`StartProcess` API honors `ProcessSpec.CleanEnv`: when it is true, the
process starts without host environment variables; explicit policy settings,
per-run variables, and sandbox-owned workspace variables are still applied.

## Explain Status

`Runtime.Explain` returns a compact operator-facing status summary:

- requested backend and platform-resolved backend
- filesystem sandbox type (`workspace-write`, `read-only`, `disabled`, or
  `external`)
- network mode (`restricted` or `enabled`)
- managed backend preflight status (`ready`, `failed`, `not-required`, or
  `unsupported`)

Explain reuses the same normalized permission profile and the same
platform-specific preflight probe used by execution. Restricted network does
not run a separate namespace or seccomp probe. `PreflightReady` means the
core backend probe succeeded; it does not prove that every reported
boundary, such as `NetworkRestricted` isolation, can be established.
Explain never runs a caller command, never acquires a workspace run lock,
and never creates a workspace. On managed profiles it may run the same
short backend probe used by execution (for example `/bin/true` under
bubblewrap) and cache that result on the Runtime, which can change when the
first execution probe happens.

`workspace-write` includes workspace special-path writes and
workspace-relative `WithWritePaths` grants. Host-absolute write grants alone
keep the filesystem type `read-only`.

When managed preflight fails, Explain still returns the configured status
fields together with a short `PreflightError`. That summary includes the error
kind, backend name, and a sanitized cause; probe stderr is omitted.

Explain is intentionally not a full policy dump. It does not list read/write
paths, no-access rules, environment inheritance, timeouts, output limits,
resource quotas, bubblewrap argv, or Seatbelt profiles. Use it to answer "which
sandbox mode is active and is the backend ready?", not to audit every grant.

Example:

```text
Sandbox
  backend:    auto -> linux-bubblewrap
  filesystem: workspace-write
  network:    restricted
  preflight:  ready
```

## Full-duplex Processes

`Runtime.StartProcess` starts a program through the same permission checks and
native sandbox backend as `RunProgram`, but returns separate stdin, stdout, and
stderr pipes. It is intended for machine protocols that need multiple request
and response exchanges while the process stays alive.

Callers must drain stdout and stderr and must call `Wait` after normal exit or
`Kill`. `Wait` releases native backend resources and the workspace run lock. A
process abandoned without `Wait` can retain backend resources and, with serial
workspace concurrency, block later runs on that workspace. Context cancellation
terminates the process but does not replace the caller's responsibility to call
`Wait`. A zero `ProcessSpec.Timeout` adds no runtime timeout; the
caller's context remains authoritative. `RunProgram` keeps its existing default
timeout behavior. Interactive input is written through `Process.Stdin()`.
