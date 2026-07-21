# Tool Safety Guard

Package `tool/safety` provides an opt-in, fail-closed guard for model-initiated tool calls. It scans the final execution input before a tool runs, returns a structured permission decision, emits metadata-only audit events, and recursively removes secrets from the final result after callbacks.

Existing applications are unchanged until they configure a guard.

## Security contract

The guard addresses seven execution risks: destructive commands, sensitive paths, network access, shell bypasses, host/interactive execution, dependency changes, and resource abuse. Secret exposure is an always-on input/output protection and cannot be disabled by policy.

The key ordering is:

1. before-tool callbacks finish modifying arguments;
2. the permission guard scans the effective arguments;
3. only `allow` reaches the executor;
4. plugins and after-tool callbacks finish modifying the result;
5. the final sanitizer recursively redacts the effective result;
6. only the sanitized result reaches events, model messages, and tool-result telemetry.

`deny` outranks `ask`, which outranks `allow`. Invalid policy or audit state never silently weakens the configured contract. A failed audit write or final sanitization returns an error and suppresses the unsafe result.

## Create a guard

```go
policyData, err := os.ReadFile("policy.yaml")
if err != nil {
    return err
}
policy, err := safety.ParsePolicy(policyData, safety.PolicyFormatAuto)
if err != nil {
    return err
}
sink, err := safety.NewJSONLSink("tool-audit.jsonl")
if err != nil {
    return err
}
defer func() {
    if err := sink.Close(); err != nil {
        log.Printf("close tool safety audit: %v", err)
    }
}()
guard, err := safety.NewGuard(policy, safety.WithAuditSink(sink))
if err != nil {
    return err
}
```

`NewDefaultGuard` enables all built-in detections with a default `allow` action. `Reload` parses and atomically installs a complete policy; a rejected reload leaves the previous snapshot active.

## Framework-wide integration

Pass the guard as the per-run permission policy:

```go
runOptions := agent.NewRunOptions(
    agent.WithToolPermissionPolicy(guard),
)
```

This route covers standard function-call processing and Graph execution, including wrapped tools and callback-provided custom results. The guard also acts as a final-result sanitizer.

## Direct built-in tool integration

Applications that call a built-in tool directly can opt in at construction:

```go
workspaceTool := workspaceexec.NewExecTool(exec,
    workspaceexec.WithSafetyGuard(guard),
)

hostTools := hostexec.NewToolSet(
    hostexec.WithSafetyGuard(guard),
)

codeTool := codeexec.NewTool(exec,
    codeexec.WithSafetyGuard(guard),
)
```

Use either framework-wide integration or the direct option for a call path. Configuring the same guard in both places is safe but produces two scans and two audit events.

For `codeexec`, scanning occurs after flexible input decoding and language validation, over the code that the executor will actually receive. The wrapper applies the most restrictive direct/invocation timeout and output profile, passes in-flight limits through `codeexecutor.ExecutionLimits`, and caps the combined returned output/file content; the local executor enforces the output budget while the child is running. For interactive write tools, an empty poll remains allowed while non-empty input or newline submission requires approval by default.

## Policy

Policies are versioned and strictly decoded. Unknown fields, duplicate keys, multiple YAML documents, trailing JSON values, invalid actions, invalid domains, and negative limits are rejected.

```yaml
version: 1
default_action: allow
profiles:
  workspace_exec:
    allowed_commands: [go, git]
    denied_commands: [curl]
    allowed_domains: [api.github.com, "*.corp.example"]
    forbidden_paths: [/etc, ~/.ssh]
    allowed_env: [CI]
    max_timeout: 2m
    max_output_bytes: 1048576
    allow_host: false
    allow_background: false
    allow_pty: false
```

Domain matching is exact. `*.corp.example` matches `build.corp.example`, but not `corp.example` or `evilcorp.example`. Proxying, destination remapping, external curl/SSH configuration, and SSH forwarding require stronger handling because they can change the effective destination.

Profile timeout and output values are ceilings, not requested defaults. Runtimes that advertise hard-limit support receive and enforce these ceilings; a configured hard limit must not be represented as enforced on an opaque backend that cannot guarantee it.

## Reports, audit, and telemetry

`Report` contains a decision, redacted findings, request digest, duration, risk level, recommendation, backend, and blocked/redacted flags. It never needs the raw request to correlate repeated activity.

`JSONLSink` serializes concurrent writes, creates or tightens files to owner-only mode on POSIX systems, rejects directories and symbolic links, and makes `Close` idempotent. Each line is an independent `AuditEvent` containing metadata and hashes rather than arguments or results.

The guard attaches a bounded set of sanitized OpenTelemetry attributes for the decision, blocked state, risk level, rule IDs, request digest, and scan duration. `tool.safety.request_sha256` is a potentially high-cardinality correlation attribute. Applications can also drop the framework's raw tool argument/result span attributes with the telemetry span-attribute policy.

## Redaction

Final-result redaction recursively handles strings, byte slices, maps, slices, arrays, and JSON-serializable structs. Sensitive keys such as `api_key`, `password`, `private_key`, and `session_token` are redacted even when their values are short. Common access tokens, bearer credentials, cloud keys, assignments, and multi-line private keys are detected by value.

Do not treat redaction as authorization. Inputs containing secret material are denied; output redaction is the last containment layer for tools that unexpectedly return a secret.

## Verification

From the repository root:

```bash
go test ./tool/safety ./tool/codeexec ./tool/hostexec ./tool/workspaceexec
go test ./internal/flow/processor ./graph ./telemetry/trace
go test -race ./tool/safety
go test -run '^$' -bench BenchmarkGuardScan500 ./tool/safety
```

Run the public, scan-only acceptance set from `examples`:

```bash
go test ./tool_safety_guard
go run ./tool_safety_guard -policy ./tool_safety_guard/tool_safety_policy.yaml -output-dir ./tool_safety_guard/output
```

Platform-specific executor tests should run on their native operating systems. A Linux container or CI runner is recommended for the repository's Unix-oriented integration tests.

## Limitations

- Static scanning is a policy boundary, not a full shell interpreter or sandbox. Use isolated runtimes and least privilege as independent layers.
- `ask` means “do not execute yet.” The host application owns the user-approval interaction and may retry only after approval.
- Third-party code executors receive a deadline and `codeexecutor.ExecutionLimits` and must honor them while running. The wrapper still caps their returned result, but cannot retroactively make an executor that ignores context cancellation stop work.
- Audit files protect integrity and local confidentiality through restrictive access, but applications remain responsible for retention, rotation, and centralized transport.
