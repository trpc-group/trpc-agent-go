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
deny or ask decisions.

`tool.PermissionPolicy` is the execution gate. It runs after arguments are
finalized and immediately before a tool is executed. `tool.FilterFunc` decides
which tools are visible; `PermissionPolicy` decides whether a requested tool call
may run.

`workspace_exec` executes inside a codeexecutor workspace. Its boundary includes
workspace paths, output limits, command policy, and environment isolation.
`hostexec` executes through the host shell and has broader risk: PTY sessions,
background processes, privilege escalation, and process cleanup must be treated
as host-level concerns. `codeexecutor` and sandbox backends provide runtime
isolation but still benefit from pre-execution scanning and audit.

## Not A Sandbox Replacement

The guard is not a sandbox. It does not enforce filesystem isolation, process
isolation, network isolation, CPU limits, memory limits, or process cleanup by
itself. Production deployments should combine this guard with sandbox or
container policies, network restrictions, clean environments, output caps, and
explicit process lifecycle management.
