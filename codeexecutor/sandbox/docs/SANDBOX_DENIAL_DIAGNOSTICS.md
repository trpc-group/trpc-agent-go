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

## Runtime Architecture

When diagnostics are first requested through `WithDiagnostics`, the runtime
performs a one-time capability probe and starts a persistent
`/usr/bin/log stream --style ndjson` monitor for the lifetime of the `Runtime`.
The monitor uses an `ENDSWITH` predicate on a per-runtime `sessionSuffix`
(`_END_<hex>_SBX`). Each diagnostics run injects a unique `runTag`
(`TRPC_RUN_<hex>_END_<hex>_SBX`) into Seatbelt deny messages when the host
supports tagging.

The first diagnostics-enabled `RunProgram` may block for up to about one second
while the probe runs. Later runs reuse the cached capability result and the
already-running monitor. The production `log stream` monitor is owned by the
`Runtime` and has no explicit shutdown hook today; it lives until the runtime's
process exits.

Probe events use a separate temporary monitor and suffix (`_PROBE_SBX`) so they
never pollute the production ring buffer.

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
    EventStreamAvailable bool // production monitor started successfully
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

## Outputs

Strongly correlated denials are returned in `Diagnostics.Denials`:

```go
for _, denial := range diagnostics.Denials {
    fmt.Printf("denied %s %s\n", denial.Operation, denial.Target)
}
```

Only log lines whose `eventMessage` contains the current `runTag` are attached to
`Diagnostics.Denials`. There are no log-based nearby hints.

The runtime does not append sandbox diagnostics to `RunResult.Stderr`. `Stderr`
contains only bytes written by the child process. Callers that need human-readable
messages should format `Diagnostics.Denials` in their CLI, UI, or agent layer.

When log streaming or deny-message tagging is unavailable, callers can inspect
`Runtime.DiagnosticsCapability()` to decide whether to show a degradation notice.

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
                Scope: sandbox.DenialFilterAll,
                Targets: []sandbox.DenialTargetMatcher{
                    {Prefix: "/dev/dtracehelper"},
                    {Prefix: "/System/Cryptexes/OS"},
                },
            },
            {
                Scope:   sandbox.DenialFilterAll,
                RawContains: []string{"duplicate report"},
            },
        },
    }),
)
```

`DenialIgnoreRule` supports `Scope`, optional `Command` substring
matching against `RunProgramSpec.Cmd` only, `Operations`, structured `Targets`
(`Exact`, `Prefix`, `Suffix`, `Glob`), and `RawContains`. `RawRegex` is
intentionally not supported.

Set `DisableAutomatic: true` to keep the three default daemon filters visible.

## Scope and Limits

- This capability is macOS-only.
- Diagnostics do not change the sandbox policy and do not ask for permission.
- If `/usr/bin/log` is unavailable or restricted, commands still run and
  diagnostics are omitted.
- macOS unified log delivery is asynchronous, so the runtime waits briefly
  after command exit for trailing denial entries.
- Default-deny events are strongly correlated only when the runtime probe
  confirms `(deny default (with message "..."))` works on the current host.
- Explicit glob and regex denies are correlated when explicit-deny tagging is
  supported.

Linux-managed sandboxing does not provide equivalent per-command denial logs in
this backend. Linux failures generally surface as the child process' normal
`EPERM` / `EACCES` errors.
