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
invalid backends and requests without recursively scannable executable input.
Output identities are canonicalized before writing so aliases cannot select
the same report/audit file. Outputs use same-directory temporary files with
mode `0600`; existing symlink targets and every existing symlink ancestor are
rejected, with only the conventional macOS `/tmp` to `/private/tmp` link
recognized explicitly. Path inspection and rename cannot eliminate every
filesystem time-of-check/time-of-use race, so production deployments should
also use a trusted output directory with appropriate ownership and mount
controls.

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
func newResultProcessor(
    guard *safety.Guard,
    sink safety.AuditSink,
) (*safety.ResultProcessor, error) {
    return safety.NewResultProcessor(
        guard,
        sink,
        safety.WithResultAuditFailureMode(safety.AuditRequired),
    )
}

func executeGuarded(
    ctx context.Context,
    guard *safety.Guard,
    processor *safety.ResultProcessor,
    request safety.Request,
    authorize func(context.Context, safety.Report) (bool, error),
    execute func(context.Context) (any, error),
) (safety.ProcessedResult, error) {
    if guard == nil || processor == nil || execute == nil {
        return safety.ProcessedResult{},
            errors.New("tool execution wrapper is not configured")
    }
    preflight := guard.Scan(request)
    switch preflight.Decision {
    case safety.DecisionAllow:
        // Continue to execution below.
    case safety.DecisionDeny:
        return safety.ProcessedResult{}, fmt.Errorf(
            "tool execution denied by safety policy: %s", preflight.RuleID,
        )
    case safety.DecisionAsk, safety.DecisionNeedsHumanReview:
        if authorize == nil {
            return safety.ProcessedResult{},
                errors.New("tool execution requires an authorizer")
        }
        approved, err := authorize(ctx, preflight)
        if err != nil {
            return safety.ProcessedResult{}, fmt.Errorf(
                "authorize tool execution: %w", err,
            )
        }
        if !approved {
            return safety.ProcessedResult{},
                errors.New("tool execution was not authorized")
        }
    default:
        return safety.ProcessedResult{},
            errors.New("tool safety returned an unsupported decision")
    }

    // execute owns normal execution and all normal callbacks. It is reached
    // only for allow or after explicit approval of a review decision.
    rawResult, executionErr := execute(ctx)
    return processor.Process(ctx, preflight, rawResult, executionErr)
}
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
