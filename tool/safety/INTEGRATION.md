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
                            └──────────┬──────────┘
                                       │  check
                                       ▼
                ┌──────────────────────────────────────────┐
                │  tool.PermissionPolicy                   │
                │  (e.g. safety.Guard)                     │
                │  ┌──────────────────────────────────┐    │
                │  │  Scanner                          │    │
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

// 1. Build a guard with the default 8 rules.
guard := safety.NewGuard()

// 2. (Optional) customize: only a subset of rules, or add an allow list.
guard = safety.NewGuard(
    safety.WithRules(
        safety.NewDangerousCommandRule(),
        safety.NewNetworkAccessRuleWithAllowlist([]string{
            "github.com", "*.npmjs.org",
        }),
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

See `examples/tool_safety_guard/main.go` for a runnable demo.
