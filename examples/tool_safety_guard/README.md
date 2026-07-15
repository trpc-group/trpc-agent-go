# Tool Safety Guard Example

This example scans tool execution requests before they run. It demonstrates
policy-driven allow, deny, and ask decisions for `workspace_exec`, host
`exec_command`, and `execute_code`-style inputs.

Run from the examples module:

```bash
cd examples
go run ./tool_safety_guard \
  -policy tool_safety_guard/tool_safety_policy.yaml \
  -samples tool_safety_guard/samples \
  -report tool_safety_guard/tool_safety_report.json \
  -audit tool_safety_guard/tool_safety_audit.jsonl
```

The CLI reads JSON samples and writes:

- `tool_safety_report.json`: structured scan reports with decision, risk level,
  rule IDs, evidence, recommendation, tool name, backend, and blocked status.
- `tool_safety_audit.jsonl`: one audit event per scan with decision, risk level,
  primary rule ID, duration, redaction status, and blocked status.

The scanner writes audit events using the policy's `audit.enabled`, `path`, and
`fail_closed` settings. The `-audit` flag overrides the configured path and
enables audit output for that run. The audit file is append-only.

The sample corpus is scan-only. Commands such as `rm -rf`, `curl`, `sudo`, and
`go install` are never executed by the example.

## Safety Boundaries

The guard uses `internal/shellsafe` to conservatively parse shell commands.
Unsupported shell constructs such as `sh -c`, `bash -c`, `eval`, backticks,
`$()`, variable expansion, redirection, and background operators become deny or
ask findings instead of default allow decisions.

Policy parsing rejects unknown fields. Command arguments and working directories
participate in semantic path and dependency checks, code block languages use the
configured allowlist, and host execution always applies its backend default
action.

`tool.PermissionPolicy` is the normal framework interception point. It runs
after tool arguments are finalized and before the tool executes. `tool.FilterFunc`
controls tool visibility, while `PermissionPolicy` controls whether a visible
tool call may run.

`workspace_exec` runs in an executor workspace and can use workspace-relative
paths, output limits, and environment scrubbing. `hostexec` runs a host shell and
has a wider blast radius: PTY sessions, background jobs, privilege escalation,
and residual processes require stricter review. `codeexec` backends and
sandboxes still need runtime isolation for filesystem, process, network, and
resource controls.

When these tools are configured with the scanner, returned output is redacted
and bounded by `resource_limits.max_output_bytes`. Each response or session poll
gets its own byte budget.

This guard cannot replace sandbox isolation. It is a pre-execution policy,
reporting, audit, and telemetry layer. Production systems should combine it with
container or sandbox enforcement, process cleanup, output limits, environment
isolation, and network controls.
