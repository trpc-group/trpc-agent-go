# Tool Execution Safety Guard

This example scans tool commands before execution and returns `allow`, `deny`,
or `ask`. Policy changes require no code changes: edit
`tool_safety_policy.json` to configure allowed and denied commands, protected
paths, network domains, time/output limits, and environment allowlists.

```bash
cd examples/tool_safety_guard
go test ./...
go run . \
  --policy tool_safety_policy.json \
  --samples samples.json \
  --report tool_safety_report.json \
  --audit tool_safety_audit.jsonl
```

The sample requests are scanned only; dangerous commands are never executed.
The checked-in report and audit examples contain all thirteen sample results;
only timestamps and scan durations are normalized.
`Guard.Wrap` is the execution-boundary adapter: it scans first, writes one audit
event, rejects `deny`/`ask`, and invokes the wrapped executor only for `allow`.
It can back an equivalent `tool.PermissionPolicy` for `workspaceexec`,
`hostexec`, or `codeexec`.

## Security layers

- `internal/shellsafe` and this guard conservatively inspect command structure.
  Unparsed wrappers, expansion, pipes, redirects, or unknown commands never
  default to allow.
- Backend values fail closed unless they are exactly `workspaceexec`, `hostexec`,
  or `codeexec`. Explicit executable paths require review; allowed bare commands
  must resolve through an executor-controlled, sanitized `PATH`. Environment
  allowlisting authorizes names only, so callers must supply trusted values.
- Network access requires a literal allowlisted HTTPS destination. Redirects,
  proxies, resolver/connect overrides, and Git URL/config overrides require
  review because the static URL can differ from the runtime destination.
- Permission/Filter policy decides whether an invocation may proceed. It is the
  pre-execution policy layer, not a runtime containment boundary.
- `workspaceexec` should use an isolated workspace, clean environment, output
  bounds, and process cleanup. A workspace still needs a container/E2B sandbox
  when executing untrusted code.
- `hostexec` runs on the host. PTY sessions, background processes, elevation,
  and process-tree cleanup carry higher risk and require review.
- `codeexecutor/container` or E2B supplies network/filesystem/process isolation.
  Static scanning cannot see every runtime behavior, while a sandbox cannot
  determine business authorization; both layers are required.
- Audit JSONL is suitable for monitoring. The result exposes
  `tool.safety.decision`, `tool.safety.risk_level`, `tool.safety.rule_id`, and
  `tool.safety.backend` as OpenTelemetry-ready span attributes.

Secrets in command evidence are redacted before reports. Production callers
must also redact executor output, logs, artifacts, span events, and errors after
execution.
