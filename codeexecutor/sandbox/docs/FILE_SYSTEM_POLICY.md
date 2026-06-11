# Sandbox File System Policy

This package uses a small file-system access lattice inspired by Codex:

- read means paths are readable but not writable.
- write means paths are readable and writable.
- no access means paths are neither readable nor writable.

Write access intentionally includes read access. The model does not support a
"writable but unreadable" path because the Linux backend cannot enforce that
shape consistently once the path is mounted into the process namespace.

## Boundary Model

The managed file-system boundary is designed around three rules:

1. Workspace paths cannot escape the workspace root. Runtime file APIs reject
   `..`, absolute paths outside the workspace, and symlinks that resolve outside
   the workspace.
2. Reads and writes are resolved through the file-system policy. More specific
   rules win first; equally specific rules use `none > write > read`.
3. Protected metadata paths are never writable, even if they are under a writable
   workspace grant. The default protected set is `.git`, `.agents`, and
   `.trpc-agent-sandbox`.

This boundary is not intended to hide the entire host file system by default.
On Linux, managed execution starts with a read-only bind mount of `/`, then adds
writable bind mounts for the workspace and any explicit external write grants.
Sensitive files that must not be readable should be covered by no-access
denials, for example through `WithNoAccessPaths` or `WithNoAccessGlobs`, or kept
outside the host paths visible to the sandbox.

## Rule Targets

Internally, file-system decisions use one of three target shapes:

- Concrete paths. Relative paths are workspace-relative; absolute paths are host
  paths. These are exposed through `WithReadPaths`, `WithWritePaths`, and
  `WithNoAccessPaths`.
- Well-known sandbox directories, such as the workspace root, `work`, `out`, and
  `skills`. These are used by built-in profiles such as
  `WorkspaceWriteProfile()`.
- Workspace-relative glob matches. These are exposed through
  `WithNoAccessGlobs` and are only valid for no-access denials.

Read and write access can target concrete paths and built-in sandbox
directories. No-access denials can additionally target workspace-relative globs.

## Resolution

When multiple rules match a path, the runtime chooses the most specific rule
first. Specificity is based on path depth: `work/secret.txt` is more specific
than `work`, and `work` is more specific than the workspace root.

If two matching rules are equally specific, access precedence breaks the tie:

```text
none > write > read
```

This keeps carve-outs predictable. For example, a profile can grant write access
to `work` but set `work/secret.txt` to `none`, making the secret neither readable
nor writable. A more specific `read` rule under a writable directory can make
that subtree read-only.

## Linux Enforcement

The Linux backend uses `bubblewrap` mount namespaces to materialize the policy:

- `--ro-bind / /` gives the command a read-only view of the host root.
- `--bind <workspace> <workspace>` makes the sandbox workspace writable.
- Explicit absolute read grants are added with `--ro-bind`.
- Explicit absolute write grants are added with `--bind`.
- Protected metadata paths are re-mounted read-only.
- No-access path, built-in directory, and glob matches are covered by unreadable masks:
  files are replaced with a zero-permission mask file, and directories are
  covered by an empty `tmpfs`.

The Go-level file APIs and Linux mount setup both derive from the same
no-access denials so a path denied by `Collect`, `PutFiles`, or `StageInputs` is
also denied to a process running inside the OS sandbox.

The implementation is intentionally path-based. It supports concrete path grants,
workspace-relative glob no-access denials, and protected metadata masks, but it
does not currently implement per-file capabilities beyond read, write, and none.

## Protected Metadata

Protected metadata is a built-in write protection for sensitive directories
inside the workspace. The default protected set is:

```text
.git
.agents
.trpc-agent-sandbox
```

Protected metadata entries are interpreted as workspace-root-relative paths.
For the default single-segment names above, protection applies to the top-level
workspace path and its children, for example `.git` and `.git/config`. It does
not match the same name at arbitrary depth, such as `vendor/.git/config`.

Protected metadata is not a replacement for no-access denials. It only prevents
writes to those paths, even when a broader rule grants workspace write access.
Use `WithNoAccessPaths` or `WithNoAccessGlobs` when a path must be neither
readable nor writable, or when a nested metadata directory must be denied
explicitly.

## Default Profile

The runtime defaults to `WorkspaceWriteProfile()`. When callers pass
`WithPermissionProfile`, that explicit profile replaces the default.

`WorkspaceWriteProfile()` starts from a read-only host view and grants writes to
the session-owned workspace directories:

- The host root is readable, giving the sandbox a read-only host view.
- The workspace root, `work`, `home`, `tmp`, `runs`, `out`, and `skills`
  directories are writable.
- Default protected metadata still blocks writes to `.git`, `.agents`, and
  `.trpc-agent-sandbox` inside the workspace.

## Public Builders

The builder API mirrors the access model:

- `WithReadPaths` grants read access to concrete paths.
- `WithWritePaths` grants write access to concrete paths.
- `WithNoAccessPaths` denies read and write access to concrete paths.
- `WithNoAccessGlobs` denies read and write access to workspace-relative glob
  matches.

Path builders accept concrete paths. Glob no-access is intentionally separate so
callers can distinguish exact path rules from workspace-relative pattern rules.

## Workspace Lifecycle and Session Policy

### Session Visibility

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

### Turn Visibility

Turns in the same session reuse the same workspace path. By default,
`SessionPolicy.Persistence` is `SessionPersistencePerSession`, so workspace
state created or modified by one turn remains visible to later turns in the same
session.

`SessionPolicy.RunConcurrency` is also `SessionRunConcurrencySerial` by default.
Program runs for the same workspace are serialized so concurrent commands do not
race against the same session file tree.

Callers can disable persistence with `WithSessionPolicy`. When
`Persistence` is `SessionPersistencePerTurn`, `Cleanup` removes the workspace
directory instead of keeping it for the next turn.

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
