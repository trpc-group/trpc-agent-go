# Tool Safety Guard

This package is the Go-side answer to issue 2002: a pre-execution safety
check that plugs into `tool.PermissionPolicy`, reuses `internal/shellsafe`,
and leaves sandbox / spawn isolation where they already belong.

## What you get

Call `safety.NewGuard(...)` and pass it to the runner with
`agent.WithToolPermissionPolicy`. Before `workspace_exec`, `exec_command`,
or code-exec tools run, the Guard returns allow / deny / ask, writes an
audit event, and (when a span is recording) sets:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

Policy lives in YAML/JSON. Changing allowlists, deny lists, denied paths,
host allowlists, env allowlists, or the timeout/output hints does not
require a code change.

## How it maps to issue 2002

| Risk class from the issue | How Guard handles it |
|---|---|
| Dangerous deletes / credential paths | `denied_commands`, `denied_paths`, plus an explicit `rm -rf` content check |
| Non-allowlisted network | host allowlist on curl/wget/ssh-style clients; scheme-less `curl host/path` included |
| Shell bypass (`$()`, backticks, `bash -c`, …) | `shellsafe.Parse` + active policy; unparsable commands are **deny** |
| hostexec PTY / long session | default `host_exec_requires_ask` |
| Dependency installs | `ask_commands` (npm/pip/apt/`go install`, …) |
| Resource abuse | long `sleep` and oversized payloads vs `max_timeout_seconds` / `max_output_bytes` |
| Secret leakage in args / reports | deny + redact `command` / findings before report/audit |

Structured report fields match the issue: decision, risk level, rule id,
evidence, recommendation, tool name, command, backend, blocked.

## Where it sits relative to the rest of the stack

```
model tool call
    -> PermissionPolicy (this Guard)     # decide allow/deny/ask, audit, OTel
    -> workspaceexec / hostexec / codeexec
         -> shellsafe policy mode        # spawn-time command allow/deny
         -> CleanEnv / workspace root    # env + cwd hardening
         -> local / container / e2B      # real isolation
```

Guard is the cheap static gate. It does **not** replace a sandbox. A
compiled binary or an obfuscated script can still do harm after an allow;
container/E2B/namespaces are what contain blast radius. It also does not
replace workspaceexec's own allow/deny + CleanEnv path (see issue 1845).

`workspace_exec` stays rooted in the workspace with scrubbed env when
policy mode is on. `exec_command` (hostexec) can keep a PTY and host
processes; that is why the default policy asks before host execution.

## Quick start

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)

events, err := runner.Run(ctx, user, session, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

Demo without an API key:

```bash
cd examples/tool_safety_guard
go run .
```

## Fail-closed notes worth knowing

- Omitted deny lists in a policy file keep `DefaultPolicy` values (overlay).
- Bare host entries are exact match only. Use `.example.com` when you
  intentionally want every subdomain.
- JSON `null` or non-object tool arguments are denied, not treated as empty.
- Secrets found in arguments are redacted on the result before anything is
  written to report/audit fixtures.
