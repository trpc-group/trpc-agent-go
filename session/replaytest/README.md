# Replay consistency harness

`replaytest` checks the replay-visible contract shared by Session, Memory,
Summary, and Track backends. It runs the same typed operations against each
backend, normalizes backend-generated values, and compares every result with a
named reference backend.

The public matrix contains 11 cases: single-turn and multi-turn messages, tool
calls, scoped state CRUD, memory persistence, summary generation/update,
summary retained-tail reconstruction, summary filter keys, tracks, concurrent
branch writes, and failure/retry recovery. Each case names an injected fault;
the unit test proves that every fault produces a blocking diff.

## Run

Root-module tests use two isolated InMemory services:

```bash
go test ./session/replaytest -count=1
```

The SQLite adapter is a separate module so the root module does not acquire a
CGO build requirement:

```bash
cd session/replaytest/sqlite
CGO_ENABLED=1 go test ./... -run TestLightweightReplayMatrix -count=1
```

`Runner.Run` returns a `Report`. Use `WriteReport` to emit the JSON artifact.
An example is available at
`testdata/session_memory_summary_track_diff_report.json`.

## Additional backends

Adapters only provide a `Backend` factory returning existing `session.Service`
and `memory.Service` implementations plus a capability map. Keep adapters for
Redis, PostgreSQL, MySQL, or ClickHouse in their existing backend modules and
gate integration tests with `REPLAYTEST_REDIS_ADDR`,
`REPLAYTEST_POSTGRES_DSN`, `REPLAYTEST_MYSQL_DSN`, or
`REPLAYTEST_CLICKHOUSE_DSN`. Missing capabilities are reported as
`unsupported` allowed diffs instead of silently skipping assertions.

`AllowedDiff` rules are deliberately strict: backend pair, JSON Pointer glob,
known rule, and a non-empty explanation are mandatory.
