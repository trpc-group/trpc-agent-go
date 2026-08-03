# Sandbox Network Policy

Sandbox profiles own network policy through `NetworkPolicy.Mode`, configured on
a profile with `WithNetworkPolicy`. Managed profiles default to
`NetworkRestricted`.

The runtime validates modes through an internal `resolvedNetworkPolicy` before
the Linux or macOS backend renders isolation. Unknown mode strings fail closed
with a policy error; they must never silently behave like `NetworkEnabled`.

## Policy Model

| Mode | Meaning | `Describe().NetworkAllowed` |
|------|---------|-----------------------------|
| `restricted` | Hard isolation; no host IP networking when enforceable | `false` |
| `controlled` | Host IP isolated; intentional IP egress only via host-managed HTTP proxy | `false` |
| `enabled` | Full host network | `true` |

- `NetworkRestricted` is the safe default for managed execution.
- `NetworkEnabled` allows the command to use the host network. On Linux this
  means the command is launched without network namespace isolation.
- `NetworkControlled` is opt-in via `WithControlledEgressProxy`. Do not add
  fields to `NetworkPolicy` for proxy endpoints (unkeyed-literal compatibility).
- **`controlled` controls host IP egress only.** It does **not** claim that the
  guest cannot reach host AF_UNIX sockets. See
  [Non-guarantees (Linux AF_UNIX)](#non-guarantees-linux-af_unix).

Profile enforcement is separate from network policy. `DangerFullAccessProfile()`
intentionally runs without local sandbox enforcement and is normalized to
`NetworkEnabled`; `ExternalSandboxProfile` declares that another system is
responsible for enforcing the requested policy.

## Linux Enforcement

### restricted / controlled isolation

When the resolved plan isolates the network (`restricted` or `controlled`), the
runtime appends `--unshare-net` to `bwrap`.

### controlled egress (CC-aligned)

Linux `controlled` matches Claude Code / sandbox-runtime shape:

```text
guest (--unshare-net)
  → HTTP_PROXY=http://127.0.0.1:<port>
  → in-sandbox egress-relay (loopback → UDS)
  → host HTTP proxy on AF_UNIX (L4/L7)
  → internet
```

- Configure with `PermissionProfile.WithControlledEgressProxy`.
- The proxy `UnixPath` must be outside every guest-writable mount. Linux
  validates both the configured path and its resolved symlink target against
  the workspace and external write grants before launching the guest.
- Provide the `egress-relay` helper via `WithControlledEgressRelayPath` or
  `TRPC_AGENT_EGRESS_RELAY`. The helper is trusted runtime code: it must be an
  executable regular file whose configured and resolved paths are outside every
  guest-writable mount.
- Guest `HTTP(S)_PROXY` values are replaced; `ALL_PROXY` and `NO_PROXY` values
  are removed.
- Start the caller-owned production proxy with `controlledegress.StartProxy`.
  It validates policy, creates the UDS with mode `0600`, caps concurrent
  connections and request-header bytes, and closes active connections on
  shutdown. `StartTestProxy` and its dial hook are test-only.
- Empty `AllowedPorts` means HTTP/HTTPS only (`80` and `443`); other ports must
  be listed explicitly. Wildcards that are public suffixes (for example
  `*.com`) are rejected when the proxy starts.
- CONNECT to a DNS name requires a TLS ClientHello whose SNI exactly matches
  the approved CONNECT host. IP-literal CONNECT targets do not have an SNI.
- Known NAT64 translation prefixes are rejected so translated RFC1918 or
  link-local IPv4 destinations cannot bypass the address filter.
- This is **not** Codex’s “enable host network, then optionally filter” model.

Policy/proxy implementation lives in
[`controlledegress`](../controlledegress/).

### Non-guarantees (Linux AF_UNIX)

Linux managed profiles use `--ro-bind / /`. That gives the guest a read-only view
of host filesystem paths, including Unix domain socket files (for example under
`/tmp` or `/var/run`). **A read-only mount does not forbid `connect(2)` on those
sockets.** `--unshare-net` blocks IP networking in the network namespace; it does
**not** block AF_UNIX IPC to host-visible socket paths.

Therefore this package does **not** guarantee:

| Claim | Status |
|-------|--------|
| Guest IP traffic can only leave through the controlled HTTP proxy | Intended for `controlled` (when clients honor `HTTP_PROXY` / CONNECT) |
| Guest cannot open arbitrary host AF_UNIX sockets (`docker.sock`, other UDS) | **Not guaranteed** under current mounts |
| `restricted` / `controlled` are equivalent to “no host IPC” | **Not guaranteed** |
| Injected `TRPC_AGENT_CONTROLLED_EGRESS=unix://…` is required for the relay | **Not required**; informational only. Relaying uses the Runtime-passed `-unix` flag and `HTTP_PROXY` |

Callers that need stronger UDS isolation must supply it outside this package
(narrower mounts, separate mount namespace policy, or host-side socket
permissions). Deployments must hide or independently protect sensitive sockets
such as `docker.sock`. Path narrowing of host UDS visibility is tracked as
follow-up hardening, not as a v1 `controlled` promise.

### enabled

When `NetworkEnabled` is selected, the backend omits `--unshare-net`.

## Lifecycle and errors (controlled)

| Component | Owner | Lifecycle |
|-----------|-------|-----------|
| Host proxy (UDS) | Caller (tests / OpenClaw) | Listen before `RunProgram`; caller closes |
| `egress-relay` | Sandbox (per-run) | Started as bwrap command wrapper; dies with the run |
| User command | Child of `egress-relay` | Exit code returned in `RunResult` |

Runtime does **not** start or stop the host proxy. Before entering the sandbox it
probes the configured Unix socket with a short `DialTimeout`. If the probe fails,
the run returns `ErrSetupFailed` and never launches the user command.

| Failure | Mapping |
|---------|---------|
| Unknown mode / controlled without absolute `UnixPath` | `ErrPolicyViolation` |
| macOS `controlled` | `ErrUnsupportedBackend` |
| Missing `egress-relay` binary | `ErrSetupFailed` |
| Host proxy UDS probe failure | `ErrSetupFailed` |
| `egress-relay` setup marker plus exit 75 or usage exit 2, without a trusted user-exit marker | `ErrSetupFailed` (controlled only; `RunProgram` and `StartProcess`) |
| L7/L4 policy deny | HTTP 403 from the proxy (not a sandbox typed error) |
| User command failure | `RunResult.ExitCode` |
| Proxy dies mid-run | Per-request failure; run is not rewritten as setup failure |

`egress-relay` writes setup failures with the `egress-relay: setup:` marker.
After a started user command fails, the relay writes a trusted user-exit marker.
That marker takes precedence over setup-like text written by the guest, so a
guest cannot make its own exit `2` or `75` look like relay setup failure.
Non-controlled profiles never perform this mapping.

Per-run audit identity is host-owned. Configure it only through
`controlledegress.WithRunIdentity` when starting the caller-owned proxy; there
is intentionally no guest-environment or run-context identity channel.
Authorization must never trust guest headers or environment values. Audit
events omit URL paths, queries, userinfo, and raw request targets.

## macOS Enforcement

- Isolated plans (`NetworkRestricted`) do not add broad network allow rules.
- `NetworkEnabled` adds broad `network-outbound` / `network-inbound` allowances.
- `NetworkControlled` currently returns an unsupported / policy error (PR B will
  align with Claude Code macOS: Seatbelt allow localhost proxy port only).

`WithMacOSWeakerNetworkIsolation` and `WithMacOSUnixSocketPaths` remain orthogonal
macOS extensions and must not be treated as controlled egress.

## Compatibility

- Do not add fields to `NetworkPolicy`, `AdditionalPermissions`, or
  `codeexecutor.Capabilities`.
- Unknown modes fail closed.
