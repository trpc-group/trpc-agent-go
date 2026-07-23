# Tool Safety Guard

Tool Safety Guard is a pre-execution safety layer for command-like tools. It
scans pending tool calls before they reach `workspace_exec`, `hostexec`, or
`codeexec`, returns an `allow`, `deny`, or `ask` decision, and emits structured
reports, JSONL audit events, and OpenTelemetry-ready attributes.

Use it for defense in depth when agents can run shell commands, scripts, code
blocks, dependency installers, or tools that may read files and access the
network.

## What it checks

The scanner is policy-driven and conservative. It covers these risk categories:

- Dangerous commands such as recursive deletion or writes to protected paths.
- Sensitive paths such as SSH keys, `.env` files, credential files, and system
  directories.
- Network egress through commands such as `curl`, `wget`, `nc`, `ssh`, or URLs
  outside the configured domain allowlist.
- Shell bypass constructs such as `sh -c`, `bash -c`, `eval`, command
  substitution, environment expansion, pipes, and redirection.
- Host execution risk such as PTY sessions, background processes, privilege
  escalation, and process lifetime issues.
- Dependency and environment changes such as `go install`, `npm install`,
  `pip install`, and package-manager installs.
- Resource abuse such as long sleeps, high timeouts, unbounded output, and
  concurrent fan-out hints.
- Secret leakage in commands, stdin, environment variables, logs, output, audit
  events, or artifacts.

The scanner also inspects `stdin` for interpreter-style executions such as
`sh`, `bash`, and `python -`, and it can scan raw JSON arguments from unknown
or open-world tools for secrets, URLs, sensitive paths, and command-like fields.

## Permission policy integration

The recommended integration point is `tool.PermissionPolicy`. Permission checks
run after the model requests a tool and arguments are finalized, but before the
tool is executed.

```go
policy, err := safety.LoadPolicyStrict("tool_safety_policy.yaml")
if err != nil {
    return err
}

guard := safety.NewPermissionPolicy(
    safety.WithPolicy(policy),
    safety.WithAuditFile("tool_safety_audit.jsonl"),
    safety.WithTelemetry(true),
)

events, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolPermissionPolicy(guard),
)
```

`WithStrictPolicyFile("tool_safety_policy.yaml")` is available when callers want
the PermissionPolicy constructor to load and validate the policy directly.

`PermissionPolicy` parses the common execution tool arguments:

- `workspace_exec`: command, cwd, env, stdin, timeout, TTY, and background.
- `workspace_write_stdin`: chars sent to a running workspace session.
- `hostexec` / `exec_command`: command, workdir, env, stdin, timeout, PTY, and
  background.
- `write_stdin`: chars sent to a running hostexec session.
- `execute_code`: Bash/Shell/Python and other code blocks.
- `skill_run`, `skill_exec`, and `skill_write_stdin`: legacy skill execution
  tools with workspace-style command, env, timeout, and stdin semantics.
- Common MCP command wrappers such as `mcp_shell`, `mcp_exec`, and
  `mcp_command`: command-shaped arguments are parsed structurally, while empty
  or nonstandard payloads fall back to raw JSON scanning.
- Unknown tools: raw JSON strings are scanned even when the configured unknown
  tool action is `allow`.

Application-specific tool names can be mapped to a backend with
`safety.WithToolBackend("custom_shell", safety.BackendWorkspaceExec)`.

`PermissionPolicy` scans the raw model-visible arguments. For `hostexec`,
tool-local options such as `WithBaseDir` are only available inside the built-in
tool. Enable `hostexec.WithSafetyScanner` when policy decisions must use the
resolved host working directory instead of the raw `workdir` argument.

`deny` prevents execution and returns a structured denied tool result. `ask`
prevents execution and returns an approval-required tool result; applications
with an approval UI should perform approval in the policy and only return
`allow` after approval.

## Policy files

Policies can be YAML or JSON. Use `LoadPolicyStrict` in production and CI so
unknown fields, invalid decisions, and negative resource limits fail fast.

```yaml
allowed_commands:
  - go
  - git
  - ls

denied_commands:
  - rm
  - sudo
  - chmod

denied_paths:
  - ~/.ssh
  - .env
  - /etc/passwd

allowed_domains:
  - github.com
  - golang.org

env_allowlist:
  - HOME

max_timeout_sec: 30
max_output_bytes: 1048576
parse_error_action: deny
shell_bypass_action: deny
dependency_install_action: ask
hostexec_tty_action: ask
unknown_tool_action: ask
audit_failure_mode: fail_closed
redact_sensitive_evidence: true
redact_sensitive_paths: true
```

The package defaults stay backward-compatible for opt-in deployments, but
production policies should normally set `unknown_tool_action` to `ask` or
`deny`, and use `audit_failure_mode: fail_closed` when a missing audit record
must block execution. `safety.ProductionPolicy()` returns stricter defaults:
unknown tools are reviewed, unsupported backends fail closed, audit failures
fail closed, and sensitive-path redaction is enabled.

Changing allowed commands, denied commands, network domains, denied paths,
timeouts, output limits, or environment keys only requires changing the policy
file. Shell startup, dynamic linker, and command search path variables such as
`PATH`, `BASH_ENV`, and `LD_PRELOAD` are always denied because they can redirect
execution before an allowlisted command runs.

## Reports, audit, and telemetry

Every scan produces a structured report with:

- `decision`
- `risk_level`
- `rule_id`
- `evidence`
- `recommendation`
- `tool_name`
- `command`
- `backend`
- `blocked`

When configured with an audit file or writer, the guard writes JSONL records
that can be consumed by monitoring or SIEM systems. The audit projection
includes tool name, decision, risk level, primary rule id, scan duration,
redaction status, and whether execution was blocked.

Audit write failures are best-effort by default. Use `audit_failure_mode:
fail_closed` or `WithAuditFailureMode(safety.AuditFailClosed)` when execution
must be denied if an audit record cannot be written.

When OpenTelemetry is enabled, the guard records these attributes on the active
span:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

When configured with `WithSafetyScanner`, the built-in `workspaceexec`,
`hostexec`, and `codeexec` tools scan returned output before it is handed back
to the model. `codeexec` also scans returned output-file content. Command
output, logs, and artifact text produced by custom tools or independent
persistence/export paths should still be scanned with `Scanner.ScanOutput`.
`PermissionPolicy` only runs before execution.

## Execution boundaries

`internal/shellsafe` is used for conservative shell parsing and command
structure checks. The safety guard builds on it with policy, path, network,
resource, host, dependency, secret, and audit rules. Commands that cannot be
parsed safely should be denied or sent for review, not allowed by default.

`workspace_exec` runs in an executor workspace. The guard checks a request
before it reaches the workspace, but workspace isolation, clean environments,
timeout enforcement, output limits, artifact handling, and process cleanup
remain the responsibility of the workspace executor.

`hostexec` runs on the host. The guard treats host PTY sessions, background
jobs, privilege escalation, and long sessions as higher-risk because they can
retain state, leave child processes, or affect the host. Runtime cleanup is
still owned by `hostexec` and the host environment.

`codeexec` and `codeexecutor` backends run code blocks in local, container, or
external runtimes. The guard scans code before execution, but container,
sandbox, E2B, filesystem, network, timeout, and output controls still provide
the runtime boundary.

## Not a sandbox replacement

Tool Safety Guard is static pre-execution analysis. It can miss obfuscated
commands, generated scripts, runtime-only data flows, indirect downloads, and
malicious dependencies. Built-in tools can scan returned output when
`WithSafetyScanner` is configured, but custom log, output, and artifact
persistence paths still need `ScanOutput`. The guard can also block legitimate
commands that look risky.

Production deployments should combine it with sandboxing, container or remote
executor isolation, clean environments, workspace permissions, network policy,
timeouts, output caps, process cleanup, artifact controls, permission policies,
and telemetry monitoring.

## Example

See `examples/tool_safety_guard` for a runnable demo, sample policy, 12
acceptance samples, deterministic `tool_safety_report.json`, and
`tool_safety_audit.jsonl`.
