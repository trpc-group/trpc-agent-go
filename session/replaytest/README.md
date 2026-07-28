# Session and Memory Replay Consistency

`replaytest` is a backend-neutral harness for checking that one deterministic
Agent trajectory produces the same durable Session and Memory view across
storage implementations. Lightweight mode compares InMemory with SQLite for
both Session and Memory and needs no credentials. `StandardCases()` exposes ten
public cases: a single turn, multiple turns, tool calls and argument
extensions, state overwrite/deletion, memory reads and writes, summary update,
summary plus later events, tracks, deterministic concurrent interleaving, and
acknowledgement-loss retry recovery.

Each case has fixed logical IDs and payloads. `Capture` reloads events, state,
memories, tracks, and the summaries returned by `ListSessions`, retaining a
summary's filter-key, boundary version, last event ID, text, and normalized
update-presence. Memory snapshots include explicit app and user scope,
identity, content, and metadata. `WithMemorySearchQueries` preserves result
order and identity while normalizing similarity scores. Track events retain
sequence and normalized timestamps; duration and latency values normalize.
This makes summary loss, overwrite, wrong scope or session, memory ordering,
and track ordering observable. JSON decoding removes map ordering. Generated
event/response IDs and clock values normalize. Backends can omit
implementation-owned fields with `Backend.PrivateMetadataPaths`, such as
`events.*.extensions.storage_private`.

Set `Backend.Unsupported` with a data-relative path such as `tracks` or
`memories.search` and a reason. Matching differences become `allowed_diff`,
and the report also contains an explicit `supported`/`unsupported` record. The
sample `testdata/session_memory_summary_track_diff_report.json` shows both a
blocking mismatch and an allowed difference.

Optional Redis, Postgres, MySQL, and ClickHouse integrations are enabled by
passing factories to `LoadOptionalBackends` with `REPLAYTEST_REDIS_URL`,
`REPLAYTEST_POSTGRES_DSN`, `REPLAYTEST_MYSQL_DSN`, or
`REPLAYTEST_CLICKHOUSE_DSN`. Unset variables produce a reported skip, so the
lightweight suite remains deterministic. Factories keep connection ownership
and credentials with the implementation-specific modules.

```go
optional, skipped, err := replaytest.LoadOptionalBackends(ctx,
    replaytest.OptionalBackend{Name: "redis", Environment: replaytest.EnvRedisURL, Factory: newRedisBackend},
    replaytest.OptionalBackend{Name: "postgres", Environment: replaytest.EnvPostgresDSN, Factory: newPostgresBackend},
)
_ = skipped // emit with the report or test log
report, err := replaytest.Run(ctx, append([]replaytest.Backend{inMemory, sqlite}, optional...), replaytest.StandardCases())
```

Run lightweight mode with:

```bash
cd session/replaytest && go test ./...
```
