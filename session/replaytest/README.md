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
memories, tracks, and summaries, retaining filter-key, boundary version, last
event ID, text, and normalized update-presence. Memory snapshots retain scope,
identity, content, and metadata. `WithMemorySearchQueries` preserves result
order while normalizing similarity scores. Tracks retain sequence; timestamps,
durations, JSON map order, and generated IDs normalize. Backends can omit
implementation-owned fields with `Backend.PrivateMetadataPaths`, such as
`events.*.extensions.storage_private`.

`Backend.Unsupported` declares unsupported paths such as `tracks`; matching
differences become `allowed_diff`.

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

Redis runs through MiniRedis by default; `REPLAYTEST_REDIS_URL` additionally
tests a configured Redis instance:

```bash
cd session/replaytest && REPLAYTEST_REDIS_URL=redis://127.0.0.1:6379 go test ./... -run TestRedisReplay
```

Set `REPLAYTEST_POSTGRES_DSN` to run the Postgres Session and Memory replay:

```bash
cd session/replaytest && REPLAYTEST_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/replaytest?sslmode=disable' go test ./... -run TestPostgresReplay
```
