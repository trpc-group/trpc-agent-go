# Sandbox Denial Diagnostics

macOS managed sandbox runs can optionally collect Seatbelt denial logs for a
single `RunProgram` call. This helps explain failures that otherwise only show
`Operation not permitted`.

Diagnostics are disabled by default. Attach a diagnostics sink to the context to
collect them for one run:

```go
ctx, diagnosticsCh := sandbox.WithDiagnostics(ctx)
res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
    Cmd:  "/bin/sh",
    Args: []string{"-c", "cat app.env"},
})
diagnostics := <-diagnosticsCh
```

Create a fresh `WithDiagnostics` context for each `RunProgram` call and receive
exactly once. The channel is never closed: do not `range` over it or wait for
closure after the result arrives; bound the wait with your own timeout or
cancellation when needed. Reusing a full channel drops later diagnostics so
`RunProgram` never blocks on delivery.

When the runtime is no longer needed, call `Runtime.Close()` (or
`CodeExecutor.Close()`) so the production `/usr/bin/log stream` monitor is
stopped. `Close` is idempotent after successful shutdown. If monitor shutdown
does not complete promptly, `Close` returns an error and retains ownership so a
later `Close` can retry. The first `Close` call permanently disables diagnostics
for that runtime; later runs cannot restart the monitor.

## Runtime Architecture

When diagnostics are first requested through `WithDiagnostics`, the runtime
performs a capability probe (unless a completed probe is already cached for the
current macOS version). When the probe confirms strong correlation is possible
(`StrongCorrelation=true`), it starts a persistent
`/usr/bin/log stream --style ndjson` monitor for the lifetime of the `Runtime`
until `Close`. The monitor uses an `ENDSWITH` predicate on a per-runtime
`sessionSuffix` (`_END_<hex>_SBX`). Each diagnostics run injects a unique
`runTag` (`TRPC_RUN_<hex>_END_<hex>_SBX`) into Seatbelt deny messages when the
host supports tagging.

Probe and monitor startup honor the `RunProgram` context / timeout. If that
context is already canceled or the run deadline expires during initialization,
`RunProgram` returns the existing cancel / `ErrTimeout` result without calling
`cmd.Start` (it does not turn a late probe into `ErrSetupFailed`). If the
context remains valid but monitoring is unavailable, the command still runs and
diagnostics are omitted.

A completed probe that finds no usable event stream is treated as a confirmed
host limitation for that runtime and is not retried. A completed probe that
finds an event stream but neither default-deny nor explicit-deny tagging
(`StrongCorrelation=false`) is likewise treated as a confirmed limitation:
capability fields still report `EventStreamAvailable=true` with both taggable
flags false, but the runtime does not start a production monitor, does not
enable per-run collection, and does not incur the settle wait. Incomplete
probes and transient production-monitor startup failures remain retryable on
later diagnostics-enabled runs. `Close` still permanently disables diagnostics
for the runtime.

The first diagnostics-enabled `RunProgram` may spend a short time probing when
the process cache is cold. Later runs reuse the cached capability result and,
when correlation is available, the already-running monitor. If the monitor
process exits, capability reporting and collection readiness become unavailable
and a later `ensureDenialMonitor` may start a new monitor, unless `Close` has
been called. Each collecting diagnostics-enabled run captures a read-only
generation handle for the ring active at start and collects from that ring even
if the log process later exits and another run installs a replacement monitor.
Generation handles do not own cancel/stop for the underlying monitor;
`Runtime.Close()` / `CodeExecutor.Close()` remain the sole shutdown owners.

When a run hits its deadline, denial collection uses a separate bounded context
so settle waits are not cut short by the already-canceled run context. Normal
completion and caller cancellation continue to inherit the run context.
Production settle waits keep the full bounded window open (default 300ms) so a
short idle gap after the first tagged denial cannot omit later correlated
events; `Truncated` still only reflects ring drops, not early settle.

Probe events use a separate temporary monitor so they never pollute the
production ring buffer. The probe monitor predicate is scoped to the probe
suffix and the two probe target paths, so ambient `Sandbox:` traffic cannot
complete the probe or overflow the probe ring. The probe waits independently for
default-deny and explicit-deny forms. For each form it requires a target-path
denial as evidence:

- target denial with the expected tag → that form is taggable
- target denial without the expected tag after the full window → that form is
  not taggable
- missing target denial → the probe stays incomplete and is not cached

`ProbeCompleted` is set only when both forms have target-level evidence.
`EventStreamAvailable` is established from those same probe-target events, not
from an arbitrary first log line. That lets a host report a completed probe with
both tag forms false when tagging is unsupported, without falsely caching that
result from unrelated sandbox noise.

The probe creates temporary empty files outside the workspace and then attempts
to read them under a probe Seatbelt profile. This is intentional: on macOS, a
missing file can fail with `ENOENT` before Seatbelt emits a useful deny event.
The probe profile uses the backend preflight policy as its startup baseline and
adds dedicated default-deny and explicit-deny messages for the probe paths.

## Diagnostics Capability

`Runtime.DiagnosticsCapability()` reports runtime-detected precision:

```go
type DiagnosticsCapability struct {
    Supported            bool // macOS managed backend
    EventStreamAvailable bool // host can deliver denial log events
    StrongCorrelation    bool // denials can be tied to this run
    ProbeCompleted       bool // probe finished reliably
    ExplicitDenyTaggable bool // explicit deny rules can carry runTag
    DefaultDenyTaggable  bool // default-deny events can carry runTag
}
```

Capabilities are probed end-to-end with `sandbox-exec` and cached per macOS
version within the process after a completed probe. `ProbeCompleted=false`
means the probe itself did not finish reliably. `ProbeCompleted=true` with
`DefaultDenyTaggable=false` or `ExplicitDenyTaggable=false` means probing
finished and that specific deny-message form was not observed on this host.
When both tag forms are false, `StrongCorrelation` is false:
`EventStreamAvailable` may still be true, but production collection stays off.

## Outputs

Strongly correlated denials are returned in `Diagnostics.Denials`:

```go
for _, denial := range diagnostics.Denials {
    fmt.Printf("denied %s %s\n", denial.Operation, denial.Target)
}
```

`Denials` contains at most one entry for each operation and target pair. When
the unified log reports the same pair more than once, the first event retained
after filtering supplies `Raw` and `Timestamp`. This coalescing does not set
`Diagnostics.Truncated`.

`Diagnostics.Truncated` is true when the shared denial ring dropped one or more
raw events after this run began and before its collection snapshot (the ring
keeps at most 100 events). Callers must not treat `Denials` as complete when
`Truncated` is true. Historical drops from earlier runs do not permanently mark
later diagnostics as truncated.

Only log lines whose `eventMessage` contains the current `runTag` are attached to
`Diagnostics.Denials`. There are no log-based nearby hints.

The runtime does not append sandbox diagnostics to `RunResult.Stderr`. `Stderr`
contains only bytes written by the child process. Callers that need human-readable
messages should format `Diagnostics.Denials` in their CLI, UI, or agent layer.

When log streaming or deny-message tagging is unavailable, callers can inspect
`Runtime.DiagnosticsCapability()` to decide whether to show a degradation notice.

`Denial.Operation` and `Denial.Target` are best-effort backend-native strings,
not a stable framework vocabulary. Values may evolve with the backend. On macOS
they are parsed from Seatbelt unified-log text: `Target` may be a filesystem
path, a Mach service name, another backend-specific value, or empty. Use them
for display and heuristic filtering; do not persist or switch on them as durable
identifiers. Filtering and per-run deduplication compare these strings as
returned by the backend.

`Denial.Raw` contains the backend's original diagnostic text. Treat it as
diagnostic-only and potentially sensitive: it may include absolute host paths,
process names, or other local system details.

## Noise Filtering

Automatic filtering is intentionally minimal and aligned with common macOS agent
sandboxes. By default, only these `mach-lookup` targets are removed:

- `mDNSResponder`
- `com.apple.diagnosticd`
- `com.apple.analyticsd`

Other noisy paths such as `/dev/dtracehelper`, `/System/Cryptexes/OS`, or
`duplicate report` entries are kept unless the caller configures additional
ignore rules.

Filtering happens at collection time. The ring buffer keeps the full event
stream so `DisableAutomatic` and configured rules can take effect.

### Configurable Filters

Use `WithDenialFilter` to add caller-specific ignore rules:

```go
rt := sandbox.NewRuntime(
    sandbox.WithDenialFilter(sandbox.DenialFilter{
        Ignore: []sandbox.DenialIgnoreRule{
            {
                Targets: []sandbox.DenialTargetMatcher{
                    {Prefix: "/dev/dtracehelper"},
                    {Prefix: "/System/Cryptexes/OS"},
                },
            },
            {
                RawContains: []string{"duplicate report"},
            },
        },
    }),
)
```

`DenialIgnoreRule` supports optional `Command` substring matching against
`RunProgramSpec.Cmd` only, `Operations`, structured `Targets`
(`Exact`, `Prefix`, `Suffix`, `Glob`), and `RawContains`. `RawRegex` is
intentionally not supported. Empty `RawContains` entries are ignored; a list
containing only empty strings is treated as unset.

`DenialTargetMatcher.Glob` uses the same doublestar dialect as sandbox
filesystem Glob rules such as `WithNoAccessGlobs`, including `**` across path
separators. Malformed Glob patterns do not match and therefore do not suppress
denials.

Zero-value `DenialFilter` keeps automatic filters enabled. Rules in `Ignore` are
disjunctive (any matching rule suppresses a denial). Within a rule, configured
constraints are conjunctive. Within one `DenialTargetMatcher`, non-empty fields
are alternatives. A rule with no effective constraints is ignored.

Set `DisableAutomatic: true` to make the three default daemon denials visible.

## Scope and Limits

- This capability is macOS-only.
- Diagnostics do not change the sandbox policy and do not ask for permission.
- If `/usr/bin/log` is unavailable or restricted, commands still run and
  diagnostics are omitted.
- macOS unified log delivery is asynchronous, so after command exit the runtime
  keeps the production settle window open for the full bounded timeout (default
  300ms, or until the collection context is canceled) before snapshotting
  tagged denials. Events that arrive after that window are not guaranteed to
  appear in `Diagnostics.Denials`.
- Default-deny events are strongly correlated only when the runtime probe
  confirms `(deny default (with message "..."))` works on the current host.
- Explicit glob and regex denies are correlated when explicit-deny tagging is
  supported.

Linux-managed sandboxing does not provide equivalent per-command denial logs in
this backend. Linux failures generally surface as the child process' normal
`EPERM` / `EACCES` errors.
