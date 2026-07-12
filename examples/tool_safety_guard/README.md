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

The twelve samples are scanned only; dangerous commands are never executed.
`Guard.Wrap` is the execution-boundary adapter: it scans first, writes one audit
event, rejects `deny`/`ask`, and invokes the wrapped executor only for `allow`.
It can back an equivalent `tool.PermissionPolicy` for `workspaceexec`,
`hostexec`, or `codeexec`.

## Security layers

- `internal/shellsafe` and this guard conservatively inspect command structure.
  Unparsed wrappers, expansion, pipes, redirects, or unknown commands never
  default to allow.
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
