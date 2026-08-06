# Integration Proof

This document provides reproducible evidence that the code review agent
works end-to-end across the full fixture matrix. Borrowed from competitor
PR #2243's `INTEGRATION_PROOF.md`.

## Quick verification

```bash
cd examples/code_review_agent

# 1. Run all tests (unit + integration)
go test ./... -count=1

# 2. Run the full fixture matrix and emit a TSV summary
./scripts/run_all_fixtures.sh
```

## Fixture matrix

Each fixture is a single `.diff` file under `testdata/fixtures/`. The
agent runs in `--dry-run` mode (no real sandbox / no LLM API calls) so
the matrix is fully deterministic and CI-friendly.

| Fixture | Expected rule | Expected conclusion | Notes |
| --- | --- | --- | --- |
| `clean.diff` | (none) | `pass` | Benign diff; all rules must stay silent |
| `security.diff` | SI-001 | `fail` | Hardcoded `sk-...` secret (critical) |
| `goroutine_leak.diff` | GL-001 | `pass` | `go func` without WaitGroup |
| `resource_leak.diff` | RL-001 | `pass` | `os.Open` without `defer Close` |
| `missing_tests.diff` | TM-001 | `pass` | New `.go` file without `_test.go` |
| `sensitive_info.diff` | SC-001 | `fail` | Private key in added line (critical) |
| `db_lifecycle.diff` | DB-001 | `pass` | `sql.Open` without `defer Close` |
| `duplicate_finding.diff` | SI-001 | `fail` | Same secret twice; deduped |
| `sandbox_failure.diff` | (none) | `pass` | Triggers no rules; sandbox skipped |
| `missing_tx_rollback.diff` | DB-002 | `pass` | `db.Begin()` without `defer Rollback` |
| `panic_in_goroutine.diff` | GL-003 | `pass` | `go func { panic(...) }` without recover |
| `cmd_injection.diff` | SC-002 | `fail` | `exec.Command("sh","-c",userInput)` (critical) |
| `sensitive_info_in_log.diff` | SC-003 | `pass` | `log.Printf("token=%s", ...)` |

## Rule catalogue (12 rules)

| Rule ID | Severity | Category | What it detects |
| --- | --- | --- | --- |
| SI-001 | critical | security | Hardcoded API key / secret |
| SC-001 | critical | security | Private key / AWS key / bearer token in added line |
| SC-002 | critical | security | Command injection via `sh -c <var>` |
| SC-003 | high | security | Sensitive identifier (password/token) in log statement |
| GL-001 | high | correctness | `go func` without WaitGroup (potential leak) |
| GL-002 | medium | correctness | `context.TODO()` / `context.Background()` not passed to child |
| GL-003 | high | correctness | `panic()` inside goroutine without `defer recover` |
| RL-001 | high | reliability | `os.Open` / `http.Get` without `defer Close` |
| EH-001 | medium | correctness | Error value ignored (`_, _ :=`) |
| TM-001 | low | quality | New `.go` file without corresponding `_test.go` |
| DB-001 | high | reliability | `sql.Open` / `.Begin` without `defer Close`/`Rollback` |
| DB-002 | high | reliability | `.Begin` without `defer Rollback`/`Commit` |

## Sandbox path

In `--repo-path` mode the agent stages the repository read-only into a
codeexecutor workspace and runs `go vet`, `staticcheck`, and `go test`
via the `code-review` skill's shell scripts. The sandbox is fail-closed:
backends without `CleanEnv` support are refused, and the permission
policy denies unknown commands.

In `--diff-file` / `--fixture-dir` / `--file-list` mode (no repo
staged), sandbox static checks are skipped and a single `StatusSkipped`
run is recorded — running `go vet` against an empty workspace only
produces noise.

## Persistence path

Every run writes:
- `<out-dir>/review_report_<taskID>.json` — machine-readable report
- `<out-dir>/review_report_<taskID>.md` — human-readable report
- `<db-path>` (SQLite) — `review_tasks`, `findings`, `sandbox_runs`,
  `permission_decisions`, `artifacts`, `reports`, `metrics` tables

The task id is embedded in the report filenames so concurrent or
repeated runs do not clobber each other's reports.

## Reproducing a single fixture

```bash
cd examples/code_review_agent
go run . --fixture-dir testdata/fixtures/security.diff \
         --out-dir ./out-security \
         --db-path ./out-security/review.db \
         --dry-run --executor local --unsafe-local
cat ./out-security/review_report_*.md
```
