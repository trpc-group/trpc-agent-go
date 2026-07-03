# macOS Sandbox Backend

The macOS backend provides managed local OS sandboxing through Apple Seatbelt by
executing commands with `/usr/bin/sandbox-exec`.

## Backend

Use `BackendMacOSSandboxExec` or `BackendAuto` on macOS. The backend string is
`macos-sandbox-exec`.

Managed profiles fail closed when `/usr/bin/sandbox-exec` is unavailable or the
host rejects Seatbelt profiles. The runtime does not automatically fall back to
`DangerFullAccessProfile`.

## File System Model

The Go-level file-system policy resolver uses the same model as Linux:

- `read` means readable but not writable.
- `write` means readable and writable.
- `none` means neither readable nor writable.
- More specific rules win before `none > write > read`.
- Protected metadata such as `.git`, `.agents`, and `.trpc-agent-sandbox` is
  readable but never writable.

The OS projection differs from Linux. Linux starts with a read-only bind mount of
`/`. macOS starts with `(deny default)`, adds selected platform read defaults,
and then adds workspace and explicit external path grants. The macOS OS
projection has backend-specific behavior for no-access globs, documented below.

## Platform Defaults

The backend includes a curated set of read-only macOS paths needed by common
tools, dynamic libraries, shells, interpreters, and system metadata. This is a
practical middle ground between strict minimalism and exposing the whole host
root, while still keeping normal command execution workable.

The baseline currently permits broad `sysctl-read` for tool compatibility. The
filesystem allow-list remains path-scoped; future iterations may narrow sysctl
access if compatibility data shows a smaller allow-list is sufficient.

Host temporary directories such as `/tmp` and `/var/folders` are not granted as
broad read roots. The runtime injects `TMPDIR`, `TMP`, and `TEMP` into the
workspace `tmp` directory, and the Seatbelt profile only allows ancestor
metadata for default temp path probes. Use `WithReadPaths` when a command must
read host temp files outside the workspace.

The defaults intentionally do not grant broad access to the user's home
directory. Use `WithReadPaths` or `WithWritePaths` for host paths outside the
workspace that commands must access.

## No-Access Globs

`WithNoAccessGlobs` is supported on macOS managed runs. The backend translates
workspace-relative glob patterns into anchored Seatbelt regular-expression
denies, for example:

```scheme
(deny file-read* (regex #"^/path/to/work/[^/]*\.env$"))
(deny file-write* (regex #"^/path/to/work/[^/]*\.env$"))
```

This is intentionally different from Linux. Linux uses startup-time bubblewrap
mount masks and may fail closed when a glob overlaps a writable mount. macOS uses
dynamic Seatbelt rules, so matching files can be denied even when they are
created after process start or live under writable roots.

No-access globs are projected as hard Seatbelt denials. A more-specific
`WithReadPaths` or `WithWritePaths` grant is not expected to reopen a path
matched by `WithNoAccessGlobs`. Use exact no-access paths when a profile needs
path-level carveouts.

## Network

The network model stays binary:

- `NetworkRestricted` does not add broad network allow rules.
- `NetworkEnabled` adds broad outbound and inbound network allow rules.

This is not the same as Linux `--unshare-net`. Linux uses a network namespace
boundary. macOS uses Seatbelt network rules plus Mach service and Unix socket
policy. The cross-platform model remains binary, while macOS-specific extension
fields expose IPC affordances that Linux does not claim to support.

`WithMacOSWeakerNetworkIsolation` allows certificate trust services such as
`com.apple.trustd.agent` for tools that need system TLS trust validation. This
can be useful for Go-based CLI tools behind proxies or custom CAs, but it
reduces isolation because Mach services can become data-exfiltration channels.

`WithMacOSUnixSocketPaths` allows AF_UNIX socket bind/connect operations for
explicit absolute socket paths. Linux keeps the namespace-level network model and
does not provide a matching Unix socket path policy in this backend. Prefer the
canonical macOS spelling for socket clients, for example `/private/tmp/...`
instead of `/tmp/...`, because Seatbelt matches Unix socket paths at connect
time.

Proxy-aware routing, per-domain/IP/port allow-lists, and loopback-only network
policies are not part of this implementation.

## Process Model

Seatbelt restrictions are inherited by child processes, so forked or exec'd
descendants remain inside the same macOS sandbox boundary. The profile permits
`process-fork` and `process-exec` so normal shell workflows can run, but the
kernel continues to enforce the same file-system, network, Mach service, and
Unix socket rules.

macOS does not provide the Linux backend's PID namespace or bubblewrap
`--die-with-parent` semantics. Runtime cancellation and timeouts rely on the
shared Unix process-group cleanup used by the package. This is useful for
terminating descendant processes, but it is not equivalent to a Linux PID
namespace.

## Capability Matrix

| Capability | Linux `linux-bubblewrap` | macOS `macos-sandbox-exec` |
| --- | --- | --- |
| OS sandbox mechanism | `bubblewrap` namespaces and mounts | Apple Seatbelt through `/usr/bin/sandbox-exec` |
| Host root visibility | Read-only bind of `/` | Selected platform defaults plus explicit grants |
| Mount namespace | Supported | Not supported |
| PID namespace | Supported with `--unshare-pid` | Not supported |
| Parent death handling | `--die-with-parent` plus process-group cleanup | Process-group cleanup only |
| Network boundary | Binary namespace model via `--unshare-net` | Binary Seatbelt model, with macOS IPC extensions |
| Mach services | Not applicable | Backend-specific allow-list |
| Unix socket path policy | Not exposed by this backend | Supported for exact absolute macOS socket paths |
| Dynamic glob deny | Static mount masks | Dynamic Seatbelt regex hard deny |
| Protected metadata | Read-only masks | Write allow exclusions |
| Resource quotas | Not implemented | Not implemented |
| PTY / ports / snapshot | Not implemented | Not implemented |

## Shell Environment

Seatbelt does not manage environment inheritance. The runtime builds the
sanitized environment with `ShellEnvironmentPolicy` and passes it directly to the
`sandbox-exec` child process.

## Known Differences From Linux

- macOS does not expose the whole host root as read-only by default.
- macOS no-access glob enforcement is dynamic; Linux enforcement is based on
  static mount masks.
- macOS uses Seatbelt rules instead of namespace and mount operations.
- macOS does not provide PID or network namespaces; process and network
  isolation are expressed through Seatbelt and process-group cleanup.
- Linux behavior and tests remain unchanged; platform differences are documented
  rather than hidden behind new public APIs.
