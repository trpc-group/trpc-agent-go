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
  means the command is launched without network namespace isolation.

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
