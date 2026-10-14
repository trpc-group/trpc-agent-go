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

Both platforms default to `ReadModeGranted`, with explicit profile grants for
runtime resources and the workspace. Linux builds a private mount view; macOS
starts with `(deny default)` and projects the effective grants into allow
filters. `WithReadMode(ReadModeHost)` enables host fallback reads on either
platform, while credential and session exclusions remain effective. The macOS OS
projection has backend-specific behavior for no-access globs, documented below.

Concrete path protections also keep ancestor directory entries stable. Seatbelt
denies unlink and rename operations on ancestors of concrete no-access paths,
effective read-only paths, protected metadata, ungranted credential locations,
and the session store. This prevents moving protected contents to a new name
outside their filters, including through symlink aliases. For example, denying
`work/config/secret` also prevents renaming `work/config`; it still permits
writing `work/config/public` when the effective policy grants that file. These
entry restrictions do not make every descendant of the ancestor read-only.

Fixed credential exclusions are checked by Seatbelt on each access. They also
cover files created at protected locations after the command starts, including
credential directories that did not exist at startup. These exclusions protect
the documented locations; arbitrary `.env`, `*.pem`, and `*.key` filenames are
not filtered automatically.

## Platform Defaults

The built-in profiles include a curated set of read-only macOS paths needed by common
tools, dynamic libraries, shells, interpreters, and system metadata. This is a
documented compatibility set for normal command execution. See the exact
[platform runtime grants](FILE_SYSTEM_POLICY.md#platform-runtime-grants).
The full `/etc` and `/private/var/db` are excluded from the default grants.

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
boundary plus an AF_UNIX/AF_VSOCK/io_uring seccomp filter under
`NetworkRestricted`, so the guest cannot create pathname or abstract Unix domain
sockets or VM sockets, including host sockets visible through explicit grants
or the host mode's `--ro-bind / /`. Anonymous stream and seqpacket socketpairs remain available.
macOS uses Seatbelt network rules plus Mach service and Unix socket policy. The
cross-platform model remains binary, while macOS-specific extension fields
expose IPC affordances that Linux does not claim to support through path
allowlists.

`WithMacOSWeakerNetworkIsolation` allows certificate trust services such as
`com.apple.trustd.agent` for tools that need system TLS trust validation. This
can be useful for Go-based CLI tools behind proxies or custom CAs, but it
reduces isolation because Mach services can become data-exfiltration channels.

`WithMacOSUnixSocketPaths` allows AF_UNIX socket bind/connect operations for
explicit absolute socket paths. Linux denies pathname and abstract AF_UNIX
sockets and AF_VSOCK under `NetworkRestricted` through seccomp, keeps anonymous
stream and seqpacket socketpairs available, and does not provide a matching Unix
socket path allowlist. Prefer the canonical macOS spelling for socket clients,
for example `/private/tmp/...` instead of `/tmp/...`, because Seatbelt matches
Unix socket paths at connect time.

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

## Denial Diagnostics

macOS can expose Seatbelt deny events through the unified log. When sandbox
denial diagnostics are requested through `WithDiagnostics`, the runtime lazily
probes host capability. When strong correlation is available it starts a
persistent `/usr/bin/log stream --style ndjson` monitor scoped to a runtime
`sessionSuffix`, and returns strongly correlated events in `Diagnostics.Denials`.
When the probe finds an event stream but neither deny form is taggable, the
capability report still exposes that result and production collection stays off.

Sandbox denial diagnostics are exposed only as structured runtime data. The
runtime does not append diagnostics to child-process stderr; callers that need
human-readable output should format `Diagnostics.Denials` in their CLI, UI, or
agent layer.

`Runtime.DiagnosticsCapability()` reports whether log streaming and deny-message
tagging were detected at runtime. Capabilities are cached per macOS version for
the process lifetime. When strong correlation is available, the production
unified-log monitor belongs to the `Runtime` and remains active until
`Runtime.Close()` (or the owning `CodeExecutor.Close()`) stops it. Callers must
close the owner when it is no longer needed.

See [`SANDBOX_DENIAL_DIAGNOSTICS.md`](SANDBOX_DENIAL_DIAGNOSTICS.md) for the
caller lifecycle responsibilities, data flow, filtering model, and limitations.

## Capability Matrix

| Capability | Linux `linux-bubblewrap` | macOS `macos-sandbox-exec` |
| --- | --- | --- |
| OS sandbox mechanism | `bubblewrap` namespaces and mounts | Apple Seatbelt through `/usr/bin/sandbox-exec` |
| Read mode | Granted by default; host mode explicitly binds `/` read-only | Granted by default; host mode adds fallback read filters |
| Mount namespace | Supported | Not supported |
| PID namespace | Supported with `--unshare-pid` | Not supported |
| Parent death handling | `--die-with-parent` plus process-group cleanup | Process-group cleanup only |
| Network boundary | `--unshare-net` plus AF_UNIX/AF_VSOCK/io_uring seccomp under restricted | Binary Seatbelt model, with macOS IPC extensions |
| Mach services | Not applicable | Backend-specific allow-list |
| Unix socket path policy | No path allowlist; restricted denies pathname/abstract AF_UNIX and AF_VSOCK via seccomp, allows stream/seqpacket socketpair | Supported for exact absolute macOS socket paths |
| Dynamic glob deny | Static mount masks | Dynamic Seatbelt regex hard deny |
| Runtime denial diagnostics | Not exposed by this backend | Supported through macOS unified log diagnostics |
| Protected metadata | Read-only masks | Write allow exclusions |
| Resource quotas | Not implemented | Not implemented |
| PTY / ports / snapshot | Not implemented | Not implemented |

## Shell Environment

Seatbelt does not manage environment inheritance. The runtime builds the
sanitized environment with `ShellEnvironmentPolicy` and passes it directly to the
`sandbox-exec` child process.

## Known Differences From Linux

- Both backends use granted mode by default; their OS mechanisms differ.
- macOS no-access glob enforcement is dynamic; Linux enforcement is based on
  static mount masks.
- macOS uses Seatbelt rules instead of namespace and mount operations.
- macOS does not provide PID or network namespaces; process and network
  isolation are expressed through Seatbelt and process-group cleanup.
- Host mode provides broad host reads on both platforms, with applicable
  credential and session protection; arbitrary host files remain readable.

macOS sandbox initialization also needs read access to the root directory
itself. The backend grants this literal runtime resource, so root entry names
can be listed; this allowance grants no access to descendant file contents.
