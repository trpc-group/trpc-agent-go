# Tool Safety Guard

`tool/safety` is an opt-in, fail-closed guard for model-initiated tool calls. It scans the final arguments after before-tool callbacks, blocks or requests approval before execution, emits metadata-only audit records, and recursively redacts the final result after all callbacks and hooks.

Existing applications keep their historical behavior until a guard is configured. Decisions always compose as `deny > ask > allow`.

## What it covers

The built-in rules cover destructive commands, sensitive paths and credential reads, non-allowlisted network access, shell bypasses, host or interactive execution, dependency changes, excessive resources, and secret exposure. Policies support strict YAML or JSON parsing, exact domains and explicit `*.example.com` wildcards, per-tool command and environment allowlists, timeouts, and hard combined stdout/stderr limits.

## Configure the framework

```go
policyData, err := os.ReadFile("tool_safety_policy.yaml")
if err != nil { return err }
policy, err := safety.ParsePolicy(policyData, safety.PolicyFormatAuto)
if err != nil { return err }
sink, err := safety.NewJSONLSink("tool_safety_audit.jsonl")
if err != nil { return err }
defer func() {
    if err := sink.Close(); err != nil {
        log.Printf("close tool safety audit: %v", err)
    }
}()
guard, err := safety.NewGuard(policy, safety.WithAuditSink(sink))
if err != nil { return err }

runOptions := agent.NewRunOptions(
    agent.WithToolPermissionPolicy(guard),
)
```

Framework-wide configuration protects every tool and is recommended. The built-in `workspaceexec`, `hostexec`, and `codeexec` tools also accept `WithSafetyGuard(guard)` for direct calls. Their local guard is discoverable by framework observability, so raw arguments are suppressed before the tool executes. Runtime timeout/output profiles compose by the most restrictive value; `codeexec` passes limits through `codeexecutor.ExecutionLimits`, the local executor enforces them while running, and the wrapper caps returned output and file content.

## Example policy

```yaml
version: 1
default_action: allow
profiles:
  workspace_exec:
    allowed_commands: [go, git]
    allowed_domains: [api.github.com, "*.trusted.example"]
    allowed_env: [CI]
    max_timeout: 2m
    max_output_bytes: 1048576
```

Policy parsing rejects unknown fields, duplicate keys, trailing documents, invalid actions, and ambiguous numeric duration units. Reloading is atomic: an invalid replacement never weakens the active policy.

## Reports, audit, and telemetry

Every `Report` has stable `decision`, `risk_level`, `rule`, `rule_ids`, `evidence`, `recommendation`, `tool_name`, `command`, `backend`, and `blocked` fields. JSONL audit events contain metadata and a request SHA-256 digest, never raw arguments or results. OpenTelemetry spans expose the decision, blocked flag, risk, sorted bounded rule IDs, backend, digest, and scan duration.

An audit or final-redaction failure blocks execution or suppresses the result. Output and error sanitizers run after ordinary callbacks, tool-result-message hooks, post-result hooks, and streaming final-state handling.

## Verify without executing dangerous samples

The example is scan-only:

```bash
go test -v ./tool_safety_guard -run TestAcceptanceMetrics
go run ./tool_safety_guard \
  -policy tool_safety_guard/tool_safety_policy.yaml \
  -output-dir tool_safety_guard/output
```

The public acceptance corpus asserts high-risk detection of at least 90%, safe false positives of at most 10%, and 100% denial for credential reads, protected destructive deletion, and non-allowlisted network access.

## Security boundary

Scanning is defense in depth, not a sandbox. Use least-privilege credentials, filesystem and network isolation, non-root containers or VMs, operating-system resource controls, and human approval for `ask`. Capability-aware workspace backends are rejected when a configured profile depends on guarantees they cannot provide. Third-party CodeExecutors must honor the supplied context limits while running; the wrapper can cap their returned result but cannot force a backend that ignores cancellation to stop.
