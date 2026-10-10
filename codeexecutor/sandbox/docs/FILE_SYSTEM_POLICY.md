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
   Native backends reject layouts they cannot enforce. On Linux, an explicit
   child grant beneath an existing no-access mask returns `ErrPolicyViolation`
   because the final parent mask would hide the granted resource.
3. Protected metadata paths are never writable, even if they are under a writable
   workspace grant. The default protected set is `.git`, `.agents`, and
   `.trpc-agent-sandbox`.

Concrete grants authorize the resolved resource; granting a parent directory
does not authorize external targets reached through descendant symlinks.
Concrete no-access rules match both lexical and resolved paths. Workspace-relative
glob checks in file APIs also examine the resolved workspace-relative path, so a
directory symlink cannot hide a denied resource. Directory staging checks each
source entry for read access and each destination for write access; copying an
allowed parent does not authorize its denied descendants. Host staging checks
both the source spelling and canonical resource. Workspace grants do not
authorize external targets reached through ancestor symlinks.

## Read Mode and Migration

`PermissionProfile.WithReadMode` selects the fallback for filesystem reads on
all platforms:

- `ReadModeGranted` permits reads only through effective filesystem grants.
  Write grants also grant reads. This is the default for an unset or empty
  mode, `ReadOnlyProfile()`, and `WorkspaceWriteProfile()`.
- `ReadModeHost` permits reads the host process could perform when no path rule
  determines access. Explicit rules, credentials, sessions, and metadata remain
  protected. It provides broader host access and does not promise confidentiality
  for arbitrary host files.
- Unknown values are preserved by the builder and rejected with
  `ErrPolicyViolation` when creating workspaces, executing programs, or using
  filesystem operations. Construction does not return an error. Cleanup remains
  available even for an invalid profile.

This is an intentional change to the previous Linux default that exposed the
host root read-only. Callers that need additional resources should grant them
with `WithReadPaths` or `WithWritePaths`, including executables installed outside
runtime roots. To opt into broad host reads explicitly:

```go
profile := sandbox.WorkspaceWriteProfile().WithReadMode(sandbox.ReadModeHost)
```

Setting the read mode preserves existing path rules; adding paths preserves
that mode. `WithReadMode(mode).WithReadPaths(path)` and the reverse order have
the same permissions. In host mode, read grants do not narrow host fallback
reads to an allowlist. The same independence applies to write and no-access
path rules.

Replace existing `WithLinuxNoHostRoot()` calls with
`WithReadMode(ReadModeGranted)`. Built-in profiles now provide that Linux
private-root behavior by default. Mode does not change environment inheritance,
networking, or the enforcement modes of `DangerFullAccessProfile` and
`ExternalSandboxProfile`.

## Platform Runtime Grants

Both built-in managed profiles explicitly include the following read-only path
rules. Missing runtime paths are optional; explicit caller grants still require
an available source. Symlinks resolve to their existing targets. Devices, process
views, filesystem traversal metadata, and platform IPC facilities are separately
constructed or allowed by the backend.

### Linux

- Directories: `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/lib32`.
- Loader configuration: `/etc/ld.so.cache`, `/etc/ld.so.conf`, `/etc/ld.so.conf.d`.
- Command shims: `/etc/alternatives` (Debian-family links such as `/usr/bin/awk`).
- Public system configuration: `/etc/passwd`, `/etc/group`, `/etc/nsswitch.conf`,
  `/etc/hosts`, `/etc/resolv.conf`, `/etc/services`, `/etc/protocols`,
  `/etc/shells`, `/etc/localtime`.
- Public certificates: `/etc/ssl/certs`, `/etc/pki/tls/certs`,
  `/etc/pki/ca-trust/extracted`.

The full `/etc` and host home directories are not default runtime grants.
Application configuration and `/etc/ssl/private` need explicit grants.

### macOS

- Applications and tool support: `/Applications`, `/Library/Apple`,
  `/Library/Developer`, `/Library/Developer/CommandLineTools`,
  `/Library/Filesystems/NetFSPlugins`, `/Library/Perl`, `/Library/Preferences`,
  `/Library/Preferences/Logging`.
- System libraries: `/System/Library/CoreServices`, `/System/Library/Frameworks`,
  `/System/Library/Perl`, `/System/Library/PrivateFrameworks`,
  `/System/Library/SubFrameworks`.
- Programs and libraries: `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`, `/usr/lib`,
  `/usr/libexec`, `/usr/share`, `/usr/local/lib`, `/opt/homebrew/lib`.
- Public configuration: `/etc/passwd`, `/etc/group`, `/etc/hosts`,
  `/etc/resolv.conf`, `/etc/localtime`, `/etc/services`, `/etc/protocols`,
  `/etc/shells`, `/etc/paths`, `/etc/paths.d`.
- Public certificates: `/etc/ssl/cert.pem`, `/etc/ssl/certs`.
- System validation data: `/private/var/db/DetachedSignatures`,
  `/private/var/db/SystemPolicyConfiguration`, `/private/var/db/crls`.
- Time zone data: `/private/var/db/timezone` (the target of `/etc/localtime`).
- Tool selection: `/private/var/select`, `/var/select`.

The full `/etc`, `/private/etc`, `/var/db`, and `/private/var/db` are not default
runtime grants. In particular, the local account database is outside these grants.
macOS allows limited metadata for host temporary-directory ancestors without
allowing their file contents. Sandbox initialization requires reading the root
directory itself; the backend permits its entry names and metadata with a
literal filter that grants no access to descendant file contents.

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

- `ReadModeGranted` starts with a private root and only binds granted runtime
  resources, workspace paths, and additional paths. It creates empty ancestors
  to reach grant destinations; it does not bind host parents to make a workspace
  reachable. `/tmp` is private by default.
- `ReadModeHost` starts with `--ro-bind / /`. Read-only mounts do not protect
  arbitrary host data or prevent Unix socket connections. Under
  `NetworkRestricted`, the AF_UNIX filter described in
  [`NETWORK_POLICY.md`](NETWORK_POLICY.md) prevents those connections.
- External parent grants are applied before session protection. The backend
  hides `<workspaceRoot>/sandbox` and restores only the current workspace and its
  descendants. Explicit symlink bind views receive the same protection. A grant
  to another session cannot reopen it. Private device and process views are also
  established after parent grants.
- Common credential paths such as `~/.ssh`, `~/.aws`, `~/.kube`, and
  `/etc/shadow` are masked unless the caller grants that exact path with
  `WithReadPaths` / `WithWritePaths`. A parent grant such as `$HOME`, or a
  child grant such as `~/.ssh/config`, does not re-open sibling credential
  files. A child grant is bind-mounted after the parent tmpfs and before
  `--remount-ro`; the empty parent is searchable but not listable in this case.
  Explicit no-access masks are applied after these credential child binds so a
  more-specific denial remains effective. Existing credential symlinks are
  resolved before the mask is mounted.
- Built-in runtime grants use `--ro-bind-try` in both modes so their more-specific
  read rules remain effective after explicit ancestor write grants. The workspace
  is restored read-only before writable mounts, supporting both built-in profiles.
  More-specific read/write mounts follow parent mounts, including workspace
  resources addressed through explicit external aliases. Missing home credential
  masks are unnecessary when their subtrees are absent from the view.
- `--bind <workspace> <workspace>` makes the sandbox workspace writable.
- Explicit absolute read grants are added with `--ro-bind`.
- Explicit absolute write grants are added with `--bind`.
- Protected metadata paths are re-mounted read-only.
- Read/write carve-outs and protections apply to every bind view of the same
  resource, including workspace root aliases and external parent aliases.
- Bind mounts follow symbolic links and cannot protect the link entry itself.
  When a metadata, credential, no-access, or effective read-only path contains a
  symlink whose physical parent remains writable, the backend returns
  `ErrPolicyViolation` before starting the program. Use a read-only parent or an
  ordinary protected path. Exact credential write grants retain their exemption;
  credential-child write grants still respect more-specific no-access rules.
- No-access path, built-in directory, and glob matches are covered by unreadable masks:
  files are replaced with a zero-permission mask file, and directories are
  covered by an empty `tmpfs`.
- Linux runs drop all process capabilities before executing the command, so a
  process cannot remove these mounts with `umount(2)`.

Credential masks are built from paths that exist when the sandbox starts. Host
scope and explicit parent grants remain live views of their host sources, so a
credential path created later by another host process is outside this
startup-time denylist guarantee. Use `ReadModeGranted` without a
parent home grant when this race is unacceptable.

The fixed credential locations are `/etc/shadow`, `/etc/gshadow`, and these
paths under the host user's home: `.ssh`, `.gnupg`, `.aws`, `.azure`,
`.config/gcloud`, `.config/gh`, `.config/hub`, `.kube`, `.docker`, `.netrc`,
`.npmrc`, `.pypirc`, `.git-credentials`, `.config/git/credentials`,
`.cargo/credentials`, `.cargo/credentials.toml`, and
`.terraform.d/credentials.tfrc.json`. Both platforms enforce the
applicable protections; exact directory or child grants remain opt-in exceptions.

The default denylist covers fixed credential locations, not arbitrary `.env`,
`*.pem`, or `*.key` files elsewhere on the host. It does not recursively scan
host directories or dynamically filter file names. Name-based masking is
intentionally not a default: `*.pem` also matches public CA bundles such as
`/etc/ssl/certs/*.pem`, workspace tasks legitimately create `key.pem` or
`.env`, and bubblewrap can only mask files that exist at startup. In granted
mode, files under the host home are inaccessible unless that directory or its
children are granted;
secrets inside shared runtime directories, the workspace, or explicit grants
still need their own policy. `WithNoAccessGlobs` only applies to the workspace
and is rejected when it overlaps Linux writable mounts.

File visibility and environment inheritance are separate. For commands that should exclude host environment variables, configure both:

```go
rt := sandbox.NewRuntime(
    sandbox.WithPermissionProfile(
        sandbox.WorkspaceWriteProfile().WithReadMode(sandbox.ReadModeGranted),
    ),
    sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
        Inherit: sandbox.ShellEnvironmentPolicyInheritNone,
    }),
)
```

Use `ReadOnlyProfile()` when commands only need to read
the workspace. Grant additional toolchain or configuration paths individually.

`StageDirectory` and `host://` inputs use the same mode fallback and path
protections. Source paths are checked throughout copying, so granting a parent
directory does not silently copy protected credentials or unrelated sessions.
Workspace file APIs remain confined to the current workspace in either mode.

The implementation is intentionally path-based. It supports concrete path grants,
workspace-relative glob no-access denials, and protected metadata masks, but it
does not currently implement per-file capabilities beyond read, write, and none.

## macOS Enforcement

The macOS backend uses Apple Seatbelt through `/usr/bin/sandbox-exec`:

- The generated SBPL starts with `(deny default)`.
- In granted mode, the profile's runtime and workspace grants provide reads.
  Host mode adds fallback reads through `/`.
- Credentials and unrelated sessions are excluded from both read and write
  filters, including parent grants. Exact credential grants can reopen the
  authorized directory or child, while sibling credentials stay protected.
  Credential exclusions also cover files created later at protected locations,
  even when those locations did not exist when the command started.
- Canonical path filters govern content access; literal symlink and ancestor
  metadata allowances permit resolving explicit path aliases.
- Workspace and explicit external path grants are projected as Seatbelt
  `allow` rules.
- Exact no-access paths are carved out from broader allows with `require-not`.
- Protected metadata paths are excluded from write allows but remain readable.
- Directory entries above concrete no-access paths, effective read-only paths,
  protected metadata, protected credential locations, and the session store cannot be
  unlinked or renamed by the sandbox. This prevents moving protected contents
  outside their path filters, including through a symlink alias. Other children
  of those directories retain their effective read/write permissions; protecting
  the ancestor entry does not make its whole subtree read-only.
- Workspace-relative no-access globs are translated into anchored Seatbelt
  regular-expression denies.

Unlike Linux, macOS glob denials are dynamic Seatbelt rules rather than
startup-time mount masks. They can apply to files created after the command
starts and to matching paths under writable roots. Linux keeps its bubblewrap
behavior and may fail closed when a glob denial overlaps a writable mount.
macOS no-access globs are hard OS-level denials: more-specific read or write
grants do not reopen glob-matched paths in the child process. Avoid combining
broad no-access globs with narrower grants when OS-level reopen semantics are
required.

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

`WorkspaceWriteProfile()` uses `ReadModeGranted` and grants writes to the
workspace root, `work`, `home`, `tmp`, `runs`, `out`, and `skills`.
`ReadOnlyProfile()` grants reads to that workspace. Both include the documented
platform runtime rules. Protected metadata blocks writes to `.git`, `.agents`,
and `.trpc-agent-sandbox` even under a writable workspace grant.

The runtime creates the well-known workspace directories during workspace
preparation. On macOS, these special directories are checked before they become
Seatbelt roots so a retained workspace cannot replace `work`, `tmp`, `home`,
`runs`, `out`, or `skills` with a symlink that points outside the workspace.

## Public Builders

The builder API mirrors the access model:

- `WithReadPaths` grants read access to concrete paths.
- `WithWritePaths` grants write access to concrete paths.
- `WithNoAccessPaths` denies read and write access to concrete paths.
- `WithNoAccessGlobs` denies read and write access to workspace-relative glob
  matches.
- `WithReadMode` selects granted-only reads or host fallback reads on every platform.

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

Non-overlapping session ids map to separate workspace directory trees, so files
written in one are not visible through another session's workspace APIs. Host
processes can still read those directories. Guest protection applies in both modes: Linux hides the session parent after
external grants and restores the current workspace; macOS excludes unrelated
session paths from its access filters. The runtime sanitizes path components in the
session id before constructing the workspace path, so an id cannot escape the
configured workspace root. Nested IDs such as `app` and `app/user/session`
are valid parent and child scopes. The parent owns the full subtree and can
access the child's files according to its profile. A child scope does not gain
access to files outside its own workspace, including parent and sibling files.
Use non-overlapping IDs for mutually isolated tenants or sessions; nested IDs
intentionally share the parent's scope.

The existing directory layout and same-ID reuse are preserved. Parent and child
workspaces can be created in either order and reopened by another Runtime.
When per-turn cleanup removes a parent workspace, its child workspaces are also
removed. Callers own scheduling and lifecycle coordination between related
scopes; per-workspace run locks do not serialize a parent against its children.

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
