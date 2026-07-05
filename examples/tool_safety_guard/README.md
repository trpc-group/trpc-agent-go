# Tool Safety Guard

This example demonstrates the `tool/safety` pre-execution guard for
command-like tools. It scans workspace shell commands, host shell commands, and
code execution blocks before the underlying tool runs.

Run the bundled samples:

```bash
cd examples/tool_safety_guard
go run . -policy tool_safety_policy.yaml -audit tool_safety_audit.jsonl
```

The example prints structured scan reports, writes the same JSON array to
`tool_safety_report.json`, and writes one monitoring-friendly JSONL event per
scan to `tool_safety_audit.jsonl`. The checked-in artifacts are the complete
output from these bundled samples.

## How It Fits

`internal/shellsafe` is the conservative shell structure parser used by the
guard. It rejects shell constructs that can bypass argv-level checks, including
`sh -c`, `bash -c`, `eval`, backticks, `$()`, variable expansion, redirection,
background operators, and unsupported control flow. The safety guard builds on
that parser and adds policy checks for dangerous commands, denied paths, network
allowlists, host execution risk, dependency installation, resource abuse, and
secret leakage.

`tool.PermissionPolicy` is the execution-time hook. Use
`safety.NewPermissionPolicy(...)` as a run-level permission policy so
`workspace_exec`, `exec_command` from hostexec, and `execute_code` are scanned
before they execute. `deny` blocks execution. `needs_human_review` and `ask`
map to the framework approval-required action.

`workspaceexec` runs inside an executor workspace. That is a safer boundary than
direct host execution, but it still needs command filtering, clean environment
handling, output limits, and workspace file controls. `hostexec` runs through
the host shell, so PTY sessions, background jobs, privilege changes, process
cleanup, and environment isolation require stricter review. `codeexecutor` can
execute code blocks through local, container, Jupyter, or other backends; code
that bridges into shell execution should still be reviewed by the guard.

When OpenTelemetry is enabled, callers can attach the report attributes returned
by `Report.SpanAttributes()`:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

## Not A Sandbox

This guard is a pre-execution filter and audit layer. It reduces obvious unsafe
tool calls and produces structured evidence, but it cannot replace sandbox
isolation. A scanner can miss novel obfuscation, language-specific behavior,
runtime side effects, compromised dependencies, or vulnerabilities in tools
after execution starts. Keep using sandbox file-system policy, network policy,
process limits, clean environments, timeouts, output caps, and artifact
redaction.

## Policy

`tool_safety_policy.yaml` controls allowed commands, denied commands, denied
paths, network allowlists, maximum timeout, maximum output size, environment
variable allowlist, and commands that require human review. Changing those
fields does not require code changes.
