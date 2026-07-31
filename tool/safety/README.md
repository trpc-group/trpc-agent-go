# Tool Safety Guard

Thin pre-execution safety layer for `trpc-agent-go` (**issue #2002**).

## What this is

`tool/safety.Guard` implements `tool.PermissionPolicy` and decides
`allow` / `deny` / `ask` **before** a tool runs. It is designed to
**reuse** `internal/shellsafe` and the existing permission hook
(`agent.WithToolPermissionPolicy`), not to invent a second shell parser
or wrap every tool (which caused capability loss in several competing PRs).

## What this is not

- Not a sandbox. Container / E2B / OS isolation still matter.
- Not a replacement for `workspaceexec` policy mode (`WithAllowedCommands`,
  `CleanEnv`, non-login shell). Those harden *spawn*; this Guard hardens
  *permission*.
- Not a claim that static scanning can see inside compiled binaries.

## Why this design (vs common #2002 failure modes)

| Failure mode in other PRs | This package |
|---|---|
| Custom lexer / regex instead of shellsafe | Always `shellsafe.Parse` + `Policy.Check` |
| Unparseable command → allow | **deny** (`shellsafe.unparseable`) |
| Omitted YAML deny lists silently disable protection | `LoadPolicyFile` overlays **DefaultPolicy** |
| `code_blocks` / stdin never scanned | `Extract` is tool-aware |
| `WrapToolSet` drops interfaces | **PermissionPolicy only** |
| Scheme-less `curl host/path` bypass | Host extraction without requiring `https://` |

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

## Policy

See `examples/tool_safety_guard/tool_safety_policy.yaml`. Changing the file
does not require code changes. Omitted deny lists keep defaults; set an
explicit empty list only when you intentionally clear them via overlay
pointers in JSON/YAML.

## Telemetry

When the context has a recording span, Guard sets:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

## Workspace vs hostexec

| Backend | Tool names (examples) | Extra policy |
|---|---|---|
| workspaceexec | `workspace_exec` | shellsafe + path/network/secret/ask |
| hostexec | `exec_command` | same, plus default **ask** (`host_exec_requires_ask`) |
| codeexec | `execute_code` (configurable name) | scans `code_blocks` text |

## Demo

```bash
cd examples/tool_safety_guard
go run .
```
