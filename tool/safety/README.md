# tool/safety

Pre-exec check that plugs into `tool.PermissionPolicy`. It leans on
`internal/shellsafe` for command shape, and it does not try to be a sandbox.

Wire it with `agent.WithToolPermissionPolicy`. Spawn isolation, CleanEnv, and
real timeouts still live in workspaceexec / hostexec / codeexecutor.

## What this actually covers

Guard looks at the JSON arguments of a tool call before the tool runs:

- shell-ish fields: `command`, `args`/`argv`, `stdin`, `code_blocks`
- file-ish fields: `path`, `file`, `file_path`, …
- `env` overrides (allowlist) and secret-looking keys (`password`, `api_key`, …)

It can return allow / deny / ask, append a JSONL audit line, and set a few
span attributes when the context already has a recording span.

It does **not**:

- kill a process that already started
- cap live stdout/stderr
- wrap every tool result for you (use `RedactText` / `RedactMap` if you need that)
- understand obfuscated code (`exec(base64.b64decode(...))` and friends)

## Issue 2002 mapping (partial on purpose)

| Topic in the issue | Here | Notes |
|---|---|---|
| Dangerous delete / credential paths | mostly | command text + path fields; not a full VFS policy |
| Network allowlist | mostly | exact host by default; `.suffix` only if you opt in |
| Shell bypass | mostly | fail closed when `shellsafe` cannot parse |
| Download \| interpreter | yes | e.g. `wget … \| python3` denied even if host is allowlisted |
| hostexec long session | partial | default ask; no process-residual detector |
| Install / mutate env | partial | `ask_commands` + install-ish code text |
| Resource abuse | scan-time only | long `sleep` / huge args / obvious `while true` → ask. Numbers in the policy are hints, not enforcement |
| Secrets in args / reports | partial | deny + redact on the scan result. Logs/artifacts after execution are on the host |
| Secrets in tool output | host-owned | PermissionPolicy never sees results |

## Decisions

Priority is deny > ask > allow. Later findings can tighten; they do not clear a deny.

Common rule ids (also land in audit / report):

- `shellsafe.unparsable`, `shellsafe.policy`
- `shell.pipe_network_to_interpreter`, `shell.pipe_to_interpreter`
- `network.denied_host`
- `path.denied`, `env.not_allowed`
- `danger.destructive_delete`
- `secret.*`
- `ask.dependency_or_mutation`
- `resource.long_sleep`, `resource.oversized_payload`, `resource.infinite_loop`
- `hostexec.long_session_risk`
- `code.install_mutation`
- `allow` (nothing matched)

## Still easy to slip past

Static checks have limits. Examples that are out of scope today:

- `execute_code` with `exec(base64.b64decode("…"))` and no obvious path/secret text
- a compiled binary that does harm after an allow
- anything that only shows up in tool **output**, not in arguments

If you need those closed, put a sandbox (or at least output redaction) next to this Guard.

## Dual policy with workspaceexec

workspaceexec may also run `shellsafe` at spawn time. Guard is an earlier,
argument-level pass. Keep the deny lists in the same ballpark or you will get
“Guard allowed, spawn denied” (or the reverse). There is no automatic sync.

## Usage

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)

events, err := runner.Run(ctx, user, session, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

Optional:

- `safety.Compose(guard, otherPolicy)` — first non-allow wins
- `safety.WithExtraRules(...)` — can only tighten
- `safety.RedactText` / `RedactMap` — for host-side result scrubbing

OTel attribute keys: `tool.safety.decision`, `tool.safety.risk_level`,
`tool.safety.rule_id`, `tool.safety.backend`.

## Policy load behavior

- Missing deny lists in a file keep `DefaultPolicy` values (overlay).
- Unknown YAML/JSON keys are rejected.
- Load failure → deny on every check (`loadErr`), auditor is not overwritten.
- Bare `allowed_hosts` entries are exact match. Use `.example.com` for suffix.

`max_timeout_seconds` / `max_output_bytes` only affect the pre-exec ask/deny
hints. Executor timeouts and output caps are separate.

## Demo

```bash
cd examples/tool_safety_guard
go run .
```

See that folder’s README for samples, `want` checks, and where reports go.
