# Session Replay Consistency Harness

`session/replaytest` is a standalone Go module that replays declarative
Session, Memory, Summary, and Track scenarios against multiple backends and
compares their normalized read-back snapshots.

## Run Lightweight Mode

Lightweight mode uses InMemory as the baseline and real SQLite as the compared
backend. SQLite uses `github.com/mattn/go-sqlite3`, so `CGO_ENABLED=1` and a C
compiler are required.

```bash
cd session/replaytest
go test ./...
```

The lightweight suite covers the public clean cases that do not require
external services, plus faulty variants that prove the comparator detects real
event, state, memory, summary, and track defects.

## Generate Report

```bash
cd session/replaytest
go run ./cmd/replayreport -cases testdata/cases -out session_memory_summary_track_diff_report.json
```

The report contains `mode`, `baselineBackend`, `backends`, roll-up `summary`,
and per-case classified diff entries with locators.

## Integration Backends

External backends are wired at runtime when their environment variable is set;
each is appended after InMemory and SQLite. A local run with neither variable
set stays on InMemory + SQLite and needs no external service.

| Backend | Environment variable | Example |
| --- | --- | --- |
| Redis | `REPLAYTEST_REDIS_ADDR` | `redis://127.0.0.1:6379` |
| Postgres | `REPLAYTEST_POSTGRES_DSN` | `postgres://user:pass@localhost:5432/db?sslmode=disable` |

Run with an external backend to produce the full three-verdict report,
including `unsupported` rows (which the local `inmemory + sqlite` run never
emits, since capability-gap categories require an integration backend):

```bash
cd session/replaytest
REPLAYTEST_REDIS_ADDR=redis://127.0.0.1:6379 \
  go run ./cmd/replayreport -cases testdata/cases -out report.json
```

Cases marked `"mode": "integration"` are skipped by lightweight mode and are
intended for these external runs.

## Diff Verdicts

Snapshots are normalized before comparison. Unmatched differences are reported
as `inconsistent`. Backend capability gaps are reported as `unsupported`.
Known harmless representation differences are reported as `allowed_diff`; for
example, SQLite may skip persisting an empty scoped summary while InMemory keeps
the empty summary entry.
