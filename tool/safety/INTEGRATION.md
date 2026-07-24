# tool/safety Integration Guide

This document explains how the `tool/safety` package integrates with
`trpc-agent-go`'s `tool.PermissionPolicy` and how to wire it into a
`Runner` so that every tool call is scanned before execution.

## Architecture

```
                            ┌─────────────────────┐
                            │   Agent / Tool      │
                            │   (model output)    │
                            └──────────┬──────────┘
                                       │ tool call
                                       ▼
                            ┌─────────────────────┐
                            │   Runner.Run        │
                            │   (per-run policy)  │
                            └──────────┬──────────┘
                                       │  check
                                       ▼
                ┌──────────────────────────────────────────┐
                │  tool.PermissionPolicy                   │
                │  (e.g. safety.Guard)                     │
                │  ┌──────────────────────────────────┐    │
                │  │  Scanner                          │    │
                │  │   ├─ ParseFailureRule            │    │
                │  │   ├─ ShellWrapperRule            │    │
                │  │   ├─ DangerousCommandRule        │    │
                │  │   ├─ NetworkAccessRule           │    │
                │  │   ├─ ShellBypassRule             │    │
                │  │   ├─ InstallAndMutateRule        │    │
                │  │   ├─ HostExecRiskRule            │    │
                │  │   ├─ ResourceAbuseRule           │    │
                │  │   ├─ SensitiveInfoLeakRule       │    │
                │  │   └─ AskForReviewRule            │    │
                │  └──────────────────────────────────┘    │
                └──────────┬───────────────────────────────┘
                           │  allow / ask / deny
                           ▼
                ┌─────────────────────┐
                │  Tool Execution     │
                └─────────────────────┘
```

`Guard` is the bridge between `tool.PermissionPolicy` and the `Scanner`.
It runs every tool call's `Arguments` through the configured `Scanner`
and translates the resulting `Decision` into a `tool.PermissionDecision`.

## Quick Start

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// 1. Build a guard with the default 10 rules.
guard := safety.NewGuard()

// 2. (Optional) load a YAML policy and wire it into the rules so the
//    deny/allow lists are enforced. A nil policy falls back to built-in
//    defaults; an empty YAML never silently disables a security check
//    because built-in deny lists are always kept.
policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
if err != nil { /* handle */ }

guard = safety.NewGuard(
    safety.WithRules(
        safety.NewDangerousCommandRuleWithPolicy(policy),
        safety.NewNetworkAccessRuleWithPolicy(policy),
        safety.NewAskForReviewRule(),
    ),
)

// 3. Plug it into a Runner per-run.
r := runner.NewRunner( /* llm, session, ... */ )
events, err := r.Run(
    context.Background(),
    "user-1",
    "session-1",
    userMessage,
    agent.WithToolPermissionPolicy(guard),
)
```

## Decision Semantics

| Scanner Decision | PermissionDecision | Effect on Tool Call |
|------------------|--------------------|---------------------|
| `DecisionDeny`   | `DenyPermission(reason)` | Skipped; denial result returned to model. |
| `DecisionAsk`    | `AskPermission(reason)`  | Skipped; approval-required result returned. |
| `DecisionAllow`  | `AllowPermission()`      | Executed normally. |

## Pre-execution Wiring (hostexec / workspaceexec)

The Guard implements `tool.PermissionPolicy` for the **Runner level**,
so every tool call is checked before the framework dispatches it. For
executor packages (`hostexec`, `workspaceexec`, ...) that already
expose a `tool.ToolSet`, two extra wiring paths are available without
modifying the executor packages themselves:

### Option A — Runner-level injection (recommended)

```go
guard := safety.NewGuard()
r.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

This is the lowest-friction path: the guard is consulted by the
framework before any tool is dispatched, and the executor packages do
not need to know about safety.

### Option B — Wrap a ToolSet (per-tool gating)

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
    "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

hostexecTS, _ := hostexec.NewToolSet()
guard := safety.NewGuard()

// Every tool exposed by hostexec is now gated by guard.
guardedTS := safety.WrapToolSet(hostexecTS, guard)

// Pass `guardedTS` to the agent / runner instead of hostexecTS.
```

`WrapToolSet` is a thin shim: it forwards `Tools(ctx)`, `Close()`, and
`Name()` to the inner tool set, but returns a wrapped slice so each
tool's `Call` is checked by the guard first. A nil guard is a no-op,
and a denied / approval-required call returns a structured
`tool.PermissionResult` instead of invoking the inner implementation.

`WrapTool` and `WrapTools` are the corresponding single-tool / slice
helpers; pick whichever shape matches your integration.

## Audit and Observability

After a scan, you can build a structured report and emit OTel span attributes:

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/safety"

report := safety.NewReport(scanResult, scanInput, "exec_command", elapsed)
event := safety.NewAuditEvent(report)

// Write to a JSONL file or your log pipeline.
io.WriteString(auditFile, event.ToolName + ... + "\n")
```

For OpenTelemetry, set the standard span attributes:

```go
span.SetAttributesString(safety.SetSpanAttributes(report))
```

## Redaction (secrets)

Pass sensitive fields through the redactor before persisting them to
audit logs or sending them back to the model:

```go
redactor := safety.NewRedactor()
redactedReport := redactor.RedactReport(report)
redactedEvent := redactor.RedactAuditEvent(safety.NewAuditEvent(report))
```

The default `Redactor` masks:
- `KEY=VALUE` style assignments (e.g. `api_key=...`, `password=...`)
- `Authorization: Bearer <token>` headers
- AWS access key ids (`AKIA...`)
- GitHub PATs (`ghp_...`, `gho_...`, etc.)
- Generic JWTs (`eyJ...`)

## Why this matters

`tool.PermissionPolicy` is the framework's official extension point for
gating tool calls. By implementing it, the safety scanner plugs into
**every** tool path (hostexec, workspaceexec, codeexec) without
modifying their internal logic. Reviewers can verify the contract in
`Guard.CheckToolPermission`, and the rule logic stays modular and
unit-testable in `Scanner.Scan`.

For executor packages that prefer a per-tool wiring shim, `WrapToolSet`
provides a one-liner that intercepts every `Call` before the
underlying implementation runs. This is a complementary path to the
Runner-level `WithToolPermissionPolicy`, not a replacement: both can
coexist (the Runner policy runs first, then the wrapped tool's
`Call` performs its own check).

See `examples/tool_safety_guard/main.go` for a runnable demo and
`tool/safety/wiring_test.go` for the wrap-tool contract tests.
