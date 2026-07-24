# Tool Execution Safety Guard example

This example scans execution requests and writes a structured report plus a
secret-minimizing JSONL audit stream. It never executes the commands in
`samples.json`.

Run it from the `examples` module:

```bash
go run ./tool_safety_guard \
  -policy tool_safety_guard/tool_safety_policy.yaml \
  -samples tool_safety_guard/samples.json \
  -report /tmp/tool_safety_report.json \
  -audit /tmp/tool_safety_audit.jsonl
```

The four paths select the YAML or JSON policy, JSON samples, report output and
audit output. Sample decoding rejects unknown fields, invalid decisions,
invalid backends and requests without executable input. Output targets are
opened through same-directory temporary files with mode `0600`; existing
symlink targets and directly symlinked parent directories are rejected (with
the conventional macOS `/tmp` to `/private/tmp` link recognized explicitly).
Renaming cannot eliminate every filesystem time-of-check/time-of-use race, so
production deployments should also use a trusted output directory with
appropriate ownership and mount controls.

The policy demonstrates that command lists, denied paths, network domains,
environment names, timeout/output limits and review commands are all changed
without recompiling the scanner. The twelve samples cover safe execution,
deletion, credential reads, network allow/deny, shell wrappers, pipelines,
dependency installation, runtime and output abuse, host PTY sessions and an
explicit `ask` decision. Checked-in fixtures normalize only scan IDs,
timestamps and durations.

## Pre-execution permission wiring

Use the safety guard at the finalized-argument permission boundary. The audit
file is owned and closed by the application, not by the sink.

```go
policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
if err != nil {
    return err
}
guard, err := safety.NewGuard(policy)
if err != nil {
    return err
}
auditFile, err := os.OpenFile(
    "tool_safety_audit.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600,
)
if err != nil {
    return err
}
defer auditFile.Close()

permission := safety.NewPermissionPolicy(
    guard,
    safety.WithAuditSink(safety.NewJSONLAuditSink(auditFile)),
    safety.WithAuditFailureMode(safety.AuditRequired),
)
run, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithToolPermissionPolicy(permission),
)
```

`DecisionNeedsHumanReview` maps to the framework's ask action. An application
with an approval UI must complete that review and issue a new authorized call;
the framework does not approve it automatically.

## Explicit post-execution processing

`ResultProcessor` is intentionally not installed into framework callbacks.
For a direct execution wrapper that retains its preflight report, invoke normal
execution and normal callbacks first, then process the final result:

```go
preflight := guard.Scan(request)
// Enforce preflight.Decision before executing.
rawResult, executionErr := executeWithNormalCallbacks(ctx, request)

processor, err := safety.NewResultProcessor(
    guard,
    safety.NewJSONLAuditSink(auditFile),
    safety.WithResultAuditFailureMode(safety.AuditRequired),
)
if err != nil {
    return err
}
processed, err := processor.Process(ctx, preflight, rawResult, executionErr)
```

The processor recursively redacts sensitive values, applies the configured cap
to the complete serialized `ProcessedResult`, and emits a correlated
`post_execute` audit event. Keep the report returned by the same direct
preflight wrapper; `PermissionPolicy` returns only a framework permission
decision and does not expose its internal report.

This mechanism is defense in depth, not a sandbox. Static parsing cannot prove
arbitrary code safe or control a process after launch. Use container or kernel
isolation, network enforcement, minimal credentials, quotas, timeouts, output
limits, process cleanup and human review as separate controls.

See the [Tool Execution Safety guide](../../docs/mkdocs/en/tool-safety.md) for
the complete threat model and backend boundaries.
