# Go Code Review Agent Example

This is a self-contained prototype for an automatic Go code review agent inspired by tRPC-Agent concepts: Skill packaging, code execution sandboxing, PermissionPolicy governance, persistent review state, telemetry-style summaries, and structured reports.

The workspace here is not the upstream `trpc-agent-go` repository, so the example keeps integration boundaries small and dependency-free. The `Store` interface and sandbox runner are shaped so they can be replaced by `session/sqlite`, `tool/codeexec`, `codeexecutor/container`, `codeexecutor/e2b`, and telemetry adapters in a real tRPC-Agent application.

## Run

```powershell
go test ./...
go run . --fixture security_sql --out-dir out/security_sql --store-path out/security_sql/store.json
go run . --fixture redaction --out-dir out/redaction --store-path out/redaction/store.json
go run . --fixture sandbox_failure --force-sandbox-failure --out-dir out/sandbox_failure --store-path out/sandbox_failure/store.json
```

Input options:

- `--diff-file path/to/change.diff`
- `--repo-path path/to/git/repo`
- `--files internal/a.go,internal/b.go`
- `--fixture clean|security_sql|goroutine_leak|resource_unclosed|db_lifecycle|missing_tests|duplicate_finding|sandbox_failure|redaction`

Runtime options:

- `--runtime fake`: deterministic dry-run sandbox, default for local tests.
- `--runtime container`: uses Docker when available, with read-only workspace mount and no network.
- `--runtime e2b` or `cube`: interface stub for remote sandbox integration.
- `--runtime local`: development fallback only, not production default.

Outputs:

- `review_report.json`
- `review_report.md`
- persistent store JSON, default `out/review_store.json`
- SQLite-compatible schema in `schema.sql`

## What A Go Developer Should Learn

To implement the full open-source version with tRPC-Agent, a Go developer should learn these areas:

1. tRPC-Agent primitives: agent lifecycle, tools, `tool/skill`, `skill load`, `skill run`, artifact handling, session/memory abstractions, filters, PermissionPolicy, telemetry hooks.
2. Sandbox execution: `tool/workspaceexec`, `tool/hostexec`, `tool/codeexec`, container runtime, E2B/Cube runtime, timeout/cancellation, output limits, read-only mounts, env allowlists, artifact allowlists.
3. Go review domain: unified diff parsing, hunk line mapping, package discovery, `go test`, `go vet`, optional `staticcheck`, context propagation, goroutine lifecycle, resource closing, error wrapping, SQL transaction lifecycle, secret scanning.
4. Persistence: SQLite schema design, task/run/finding/report relations, idempotent migrations, query by task id, redacted evidence, report artifacts.
5. Governance and observability: permission decisions, deny/ask/needs-human-review states, deduplication, confidence thresholds, severity statistics, latency, exception distributions, replayable audit records.
6. Testability: deterministic rule-only mode, fake model mode, fixture diffs, sandbox failure tests, hidden-sample friendly rules, no API key requirement.

## Fixture Matrix

| Fixture | Purpose |
| --- | --- |
| `clean` | no blocking issue and test update present |
| `security_sql` | SQL formatting risk and rows lifecycle risk |
| `goroutine_leak` | goroutine cancellation and `time.Tick` leak |
| `resource_unclosed` | file opened without close path |
| `db_lifecycle` | transaction without commit/rollback |
| `missing_tests` | production behavior without test update |
| `duplicate_finding` | deduplication and secret redaction |
| `sandbox_failure` | sandbox failure should not crash review |
| `redaction` | reports and store must not contain raw password/token |

## Production Integration Notes

- Replace `JSONStore` with a `database/sql` implementation backed by `session/sqlite` or another SQL backend. The included `schema.sql` is the minimum relational shape.
- Replace `SandboxRunner` with `tool/codeexec` plus `codeexecutor/container` or E2B/Cube runtime. Keep `local` behind a development-only flag.
- Connect `PermissionPolicy.Decide` to tRPC-Agent filter/permission hooks and emit telemetry spans for every tool call and rule pass.
- If adding LLM review, keep deterministic rules as the first pass and only let low-confidence cases ask the model. Store model evidence separately and keep secret redaction on every boundary.
