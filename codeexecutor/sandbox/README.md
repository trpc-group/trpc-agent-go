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

### Boundary model

The managed file-system boundary is designed around three rules:

1. Workspace paths cannot escape the workspace root. Runtime file APIs reject
   `..`, absolute paths outside the workspace, and symlinks that resolve outside
   the workspace.
2. Reads and writes are resolved through the file-system policy. More specific
   rules win first; equally specific rules use `none > write > read`.
3. Protected metadata paths are never writable, even if they are under a writable
   workspace grant. The default protected set is `.git`, `.agents`, `.codex`,
   and `.trpc-agent-sandbox`.

This boundary is not intended to hide the entire host file system by default.
On Linux, managed execution starts with a read-only bind mount of `/`, then adds
writable bind mounts for the workspace and any explicit external write grants.
Sensitive files that must not be readable should be covered by `none` rules or
kept outside the host paths visible to the sandbox.

### Linux implementation

The Linux backend uses `bubblewrap` mount namespaces to materialize the policy:

- `--ro-bind / /` gives the command a read-only view of the host root.
- `--bind <workspace> <workspace>` makes the sandbox workspace writable.
- Explicit absolute read grants are added with `--ro-bind`.
- Explicit absolute write grants are added with `--bind`.
- Protected metadata paths are re-mounted read-only.
- `none` file matches are replaced with a zero-permission mask file, and `none`
  directory matches are covered with an empty `tmpfs`.

The implementation is intentionally path-based. It supports concrete path grants,
workspace-relative glob `none` rules, and protected metadata masks, but it does
not currently implement per-file capabilities beyond read, write, and none.

### Session visibility

Each sandbox session gets a deterministic workspace path under the runtime
workspace root:

```text
<workspaceRoot>/sandbox/<sanitized exec/session id>
```

The default workspace root is `${TMPDIR}/trpc-agent-go-sandbox`, and callers can
override it with `WithWorkspaceRoot`.

Different session ids map to different workspace directories, so files written in
one session are not visible through another session's workspace. This is the
session-level file-system boundary. The runtime sanitizes path components in the
session id before constructing the workspace path, so an id cannot escape the
configured workspace root.

This boundary is directory isolation, not a separate storage backend. If a
profile grants read or write access to an absolute host path outside the
workspace, sessions with the same external grant can still observe that shared
host path.

### Turn visibility

Turns in the same session reuse the same workspace path. By default,
`SessionPolicy.PersistFilesAcrossTurns` is enabled, so files created or modified
by one turn remain visible to later turns in the same session.

`SessionPolicy.MutatingCommandsSerial` is also enabled by default. Program runs
for the same workspace are serialized so concurrent mutating commands do not
race against the same session file tree.

Callers can disable persistence with `WithSessionPolicy`. When
`PersistFilesAcrossTurns` is false, `Cleanup` removes the workspace directory
instead of keeping it for the next turn.

### Lifecycle

`CreateWorkspace` creates or opens the deterministic session workspace. It
ensures the standard layout exists, creates `home` and `tmp`, and then applies
the optional manifest.

Manifest files are materialized append-only: if a manifest file already exists,
`CreateWorkspace` leaves it in place rather than overwriting live session state.
Manifest `EphemeralPaths` are removed each time the workspace is created or
reopened, which gives callers a scoped way to reset selected paths while keeping
the rest of the session persistent.

`Cleanup` is policy-driven. With the default persistent session policy it is a
no-op for files, preserving the workspace for future turns. With persistence
disabled, it deletes the workspace directory. There is no automatic TTL or quota
cleanup in this backend; callers that use persistent sessions should manage
workspace retention outside the runtime.
