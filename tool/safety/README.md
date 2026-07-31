# Tool Safety Guard

Pre-execution permission layer for `trpc-agent-go` (issue **#2002**).

## Design choice (why not another scanner empire)

`workspaceexec` already speaks `internal/shellsafe` for allow/deny at spawn
time, and the runner already has `tool.PermissionPolicy`. Most competing
#2002 patches re-implement parsing and wrap every tool. That creates two
problems we kept seeing in review threads on sibling PRs:

1. **Scanned text ≠ executed text** — a custom regex admits something
   shellsafe would reject, or vice versa.
2. **`WrapToolSet` capability loss** — wrappers drop `PermissionChecker`,
   stream, or state-delta interfaces.

This package does the boring, harder-to-get-wrong thing: **PermissionPolicy
only**, and **shellsafe is the only shell parser**. Extra rules (paths,
hosts, secrets, ask-commands, hostexec ask) sit *on top of* that parse,
never beside a second lexer.

## Fail-closed contracts

| Situation | Behavior |
|---|---|
| `shellsafe.Parse` error (`$()`, backticks, redirects, …) | **deny** (`shellsafe.unparsable`) |
| Policy YAML omits `denied_commands` | Keep **DefaultPolicy** denies (overlay, not replace) |
| Bare allowlist host `api.github.com` | Exact match only — **not** `evil.api.github.com` |
| Suffix wildcards | Opt-in via leading-dot entries (`.github.com`) |
| Secret / token hit | deny, and **redact `Result.Command`** before report/audit |

`MaxTimeoutSeconds` / `MaxOutputBytes` in the policy file are **advisory
metadata** for operators and reports. Hard limits still belong to
workspaceexec / hostexec / codeexecutor — claiming otherwise would be a
documentation lie.

## Wiring

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)

events, err := runner.Run(ctx, user, session, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

Compose with other policies in the host if needed; this Guard returns
allow for non-exec tools with an empty payload so it can sit on a shared
runner without starving unrelated tools.

## What Guard is not

- Not a sandbox (Docker / E2B / namespaces).
- Not a substitute for workspaceexec policy mode + `CleanEnv` (#1845).
- Not strong enough alone against compiled binaries or intentionally
  obfuscated scripts — that is why the README insists on defense in depth.

## Telemetry

Recording spans get:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

## Demo

```bash
cd examples/tool_safety_guard
go run .
```
