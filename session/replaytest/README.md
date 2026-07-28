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

Optional integrations use `REPLAYTEST_REDIS_URL`, `REPLAYTEST_POSTGRES_DSN`,
`REPLAYTEST_MYSQL_DSN`, or `REPLAYTEST_CLICKHOUSE_DSN`. Unset variables
produce reported skips; factories retain connection ownership and credentials.

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
cd test && go test ./replaytest
```

`REPLAYTEST_REDIS_URL` tests a configured Redis instance:

```bash
cd test && REPLAYTEST_REDIS_URL=redis://127.0.0.1:6379 go test ./replaytest -run TestRedisReplay
```

Set `REPLAYTEST_POSTGRES_DSN` to run the Postgres Session and Memory replay:

```bash
cd test && REPLAYTEST_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/replaytest?sslmode=disable' go test ./replaytest -run TestPostgresReplay
```

Set `REPLAYTEST_MYSQL_DSN` to run the MySQL Session and Memory replay:

```bash
cd test && REPLAYTEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/replaytest?parseTime=true' go test ./replaytest -run TestMySQLReplay
```

Set `REPLAYTEST_CLICKHOUSE_DSN` for ClickHouse Session and Memory replay. ClickHouse 24.8 requires its experimental JSON setting:

```bash
cd test && REPLAYTEST_CLICKHOUSE_DSN='clickhouse://user:password@127.0.0.1:9000/database?allow_experimental_json_type=1' go test ./replaytest -run 'TestClickHouse(Replay|MemoryLifecycle)'
```
