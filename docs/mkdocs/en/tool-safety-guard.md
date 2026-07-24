# Tool Safety Guard

Tool Safety Guard scans tool execution requests before they run. It is designed
for tools that can execute commands, scripts, code blocks, MCP tools, or skills.

The guard produces an allow, deny, or ask decision and a structured report with
the decision, risk level, rule ID, evidence, recommendation, tool name, command,
backend, and blocked status. It can also write JSONL audit events and expose
bounded telemetry attributes such as `tool.safety.decision`,
`tool.safety.risk_level`, `tool.safety.rule_id`, and `tool.safety.backend`.

## Relationship To Existing Controls

`internal/shellsafe` provides conservative shell parsing. It rejects constructs
that can bypass command policies, including shell wrappers, command
substitution, variable expansion, redirection, subshells, and background
operators. The safety guard uses that parser and treats unsafe parse failures as
deny or ask decisions. It also reuses shellsafe's non-overridable wrapper set,
so process runners such as `env`, `xargs`, `timeout`, and `nohup` cannot hide a
nested command. Bare allowlist entries match only bare executables; `git` does
not implicitly allow `./git` or `/tmp/git`.

`tool.PermissionPolicy` is the execution gate. It runs after arguments are
finalized and immediately before a tool is executed. `tool.FilterFunc` decides
which tools are visible; `PermissionPolicy` decides whether a requested tool call
may run.

`workspace_exec` executes inside a codeexecutor workspace. Its boundary includes
workspace paths, output limits, command policy, and environment isolation.
`hostexec` executes through the host shell and has broader risk: PTY sessions,
background processes, privilege escalation, and process cleanup must be treated
as host-level concerns. Every host request is subject to
`backend_rules.hostexec.default_action`, including ordinary foreground commands.
`codeexecutor` and sandbox backends provide runtime isolation but still benefit
from pre-execution scanning and audit. Code block languages are checked against
`backend_rules.codeexec.allowed_languages` before execution.

Policy loading is strict: unknown JSON or YAML fields and trailing documents are
rejected. This prevents misspelled security settings from being silently
ignored. When `audit.enabled` is true, `NewScanner` opens `audit.path` and writes
one JSONL event per scan. Audit creation or write failures block execution when
`audit.fail_closed` is true. Call `Scanner.Close` when the scanner is no longer
needed. `redaction.enabled` defaults to true and an explicit false value is
preserved.

The workspace, host, and code execution adapters apply the scanner's redaction
and `resource_limits.max_output_bytes` budget to user-visible output. The cap is
per tool response; each poll of a long-running session is bounded separately.

## Not A Sandbox Replacement

The guard is not a sandbox. It does not enforce filesystem isolation, process
isolation, network isolation, CPU limits, memory limits, or process cleanup by
itself. Production deployments should combine this guard with sandbox or
container policies, network restrictions, clean environments, output caps, and
explicit process lifecycle management.
