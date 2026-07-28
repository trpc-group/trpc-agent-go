# Code Review Agent — Design Document

## Skill Design

The code review skill (`skills/code-review/`) packages reusable review workflows as a SKILL.md with rules documentation and check scripts. Rules are implemented as Go types implementing the `CRRule` interface, registered at startup via `RuleRegistry`. Each rule targets a specific category (security, goroutine leak, resource leak, error handling, test coverage, DB lifecycle) and produces structured `Finding` values with severity, evidence, and recommendations.

## Sandbox Isolation Strategy

Sandbox execution uses the `codeexecutor.Engine` interface, supporting container (Docker), E2B cloud sandbox, and local runtimes. The `SandboxManager` creates an isolated workspace per task, copies diff files into it, runs programs (`go vet`, `staticcheck`, custom scripts) with configurable timeouts and output limits, and collects results. Container/E2B are the production defaults; local is only a development fallback.

## Permission / Filter Strategy

The `CRPermissionPolicy` wraps the sandbox command allow/deny lists and implements the `tool.PermissionPolicy` interface. High-risk commands (rm, curl, sudo, apt) are statically denied. Unknown commands are either denied or require human review (`ask`), ensuring no untrusted command reaches the sandbox without governance.

## Monitoring Fields

Each review records: total duration, sandbox execution duration, tool call count, permission deny/ask counts, finding count by severity, and error type distribution. These are embedded in `MonitoringSummary` and persisted alongside the task.

## Database Schema

Six tables with `cr_` prefix: `cr_tasks` (task metadata and stats), `cr_findings` (structured findings with dedup support), `cr_sandbox_runs` (sandbox execution records), `cr_permission_decisions` (permission decisions), `cr_reports` (JSON/Markdown report content), and `cr_artifacts` (sandbox output files). The `Store` interface abstracts storage, with SQLite as the default implementation.

## Dedup and Noise Reduction

The `DedupEngine` removes findings with the same file, line, and rule ID. Low-confidence findings are separated into `warnings` and capped at a configurable maximum, preventing noise from overwhelming high-confidence results.

## Security Boundaries

Sandbox execution enforces per-command timeouts, output size limits, environment variable whitelisting, and sensitive information redaction (API keys, tokens, private keys). All commands are checked against allow/deny lists before execution. Failures are captured as structured errors and do not crash the overall review.
