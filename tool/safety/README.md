# Tool Safety Guard

Pre-execution `tool.PermissionPolicy` for issue 2002. It reuses
`internal/shellsafe`, fails closed on garbage input, and leaves sandbox /
CleanEnv / executor timeouts where they already belong.

## Scope (honest)

This package is an **exec-and-path argument gate**, not a full runtime
security boundary.

| Covered now | Explicitly out of band / host responsibility |
|---|---|
| `command` + `args`/`argv`, `stdin`, `code_blocks` | Live process timeout / kill |
| File-like JSON fields (`path`, `file`, `file_path`, …) | Live stdout/stderr size caps |
| Secret-shaped args / JSON keys; report redaction | Automatic result wrapping for every tool |
| Network allowlist + `curl\|python` style pipes | Semantic understanding of obfuscated code |
| hostexec default ask; install/sleep/oversized ask | Replacing container / E2B / namespaces |

`RedactText` / `RedactMap` exist so hosts can scrub outputs without
`WrapToolSet`. PermissionPolicy cannot see tool results by design.

## What you get

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)
events, err := runner.Run(ctx, user, session, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

Span attributes when a span is recording:

- `tool.safety.decision`
- `tool.safety.risk_level`
- `tool.safety.rule_id`
- `tool.safety.backend`

## How it maps to issue 2002

| Risk class | Guard behavior |
|---|---|
| Dangerous deletes / credential paths | deny lists + path fields + `rm -rf` content check |
| Non-allowlisted network | host allowlist; exact match by default |
| Shell bypass | `shellsafe.Parse` + wrapper denies; unparsable → deny |
| Download-to-interpreter | `curl\|wget … \| python/node/…` → deny even if host allowlisted |
| hostexec PTY / long session | default `host_exec_requires_ask` |
| Dependency installs | `ask_commands` |
| Resource abuse (scan-time) | long `sleep` / oversized args / obvious `while true` → ask (hints, not process enforcement) |
| Secret leakage in args / reports | deny + redact; export helpers for post-exec scrubbing |

## Design choices

- **PermissionPolicy only** — compose with `safety.Compose`; avoid WrapToolSet capability loss.
- **Reuse `shellsafe`** — no second shell parser.
- **Fail closed** — null/malformed args, unknown policy keys, load errors, unparsable commands deny.
- **Exact host allowlist** — use `.example.com` only when suffix wildcards are intentional.
- **Tighten-only extensions** — `WithExtraRules`.

## Stack position

```text
model tool call
    -> PermissionPolicy (this Guard)     # decide allow/deny/ask, audit, OTel
    -> workspaceexec / hostexec / codeexec
         -> shellsafe policy mode        # spawn-time command allow/deny
         -> CleanEnv / workspace root    # env + cwd hardening
         -> local / container / e2B      # real isolation
```

## Demo

```bash
cd examples/tool_safety_guard
go run .
```

Samples live in `tool_safety_samples.json` (oversized stdin appended at runtime).

## Fail-closed notes

- Omitted deny lists keep `DefaultPolicy` values (overlay).
- Bare hosts are exact match only.
- JSON `null` / non-object arguments deny.
- `max_timeout_seconds` / `max_output_bytes` are **scan hints** that drive ask; they do not replace executor limits.
