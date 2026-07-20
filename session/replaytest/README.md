# Replay consistency harness

`replaytest` checks the replay-visible contract shared by Session, Memory,
Summary, and Track backends. It runs the same typed operations against each
backend, normalizes backend-generated values, and compares every result with a
named reference backend or with every other backend in oracle-free consensus
mode.

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

Reference mode is the zero-value default and remains convenient for two
backends:

```go
report, err := (replaytest.Runner{Reference: "inmemory"}).Run(ctx, cases, backends)
```

For three or more independent implementations, consensus mode avoids assuming
that the reference is correct:

```go
report, err := (replaytest.Runner{Mode: replaytest.ComparisonConsensus}).Run(ctx, cases, backends)
```

Consensus mode compares every backend pair in stable name order. It reports a
single `outlier` only when that backend disagrees with every other backend and
all remaining backends agree with each other. Two-backend disagreements,
split votes, and non-transitive results are `ambiguous`; fewer than two
successful comparable backends are `insufficient`. Execution errors and
unsupported capabilities stay outside the consensus matrix and remain visible
as ordinary report diffs.

## Additional backends

Adapters only provide a `Backend` factory returning existing `session.Service`
and `memory.Service` implementations plus a capability map. Keep adapters for
Redis, PostgreSQL, MySQL, or ClickHouse in their existing backend modules and
gate integration tests with `REPLAYTEST_REDIS_ADDR`,
`REPLAYTEST_POSTGRES_DSN`, `REPLAYTEST_MYSQL_DSN`, or
`REPLAYTEST_CLICKHOUSE_DSN`. Missing capabilities are reported as
`unsupported` allowed diffs instead of silently skipping assertions.

`AllowedDiff` rules are deliberately strict: an unordered backend pair, JSON
Pointer glob, known rule, and a non-empty explanation are mandatory. Pairwise
agreement is based on blocking differences, so an explicitly allowed
difference does not create a false outlier.
