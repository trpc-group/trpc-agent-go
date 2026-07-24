# Replay Consistency Harness

`session/replaytest` provides a backend-neutral replay harness for session,
memory, summary, and track consistency checks. The default lightweight test
compares InMemory with a JSON round-trip persistent simulator without modifying
existing repository go.mod files. The standalone `tests/replay_consistency`
module compares InMemory with SQLite when CGO is available.

## Lightweight Run

```bash
go test ./session/replaytest
cd tests/replay_consistency && go test ./...
cd tests/replay_consistency && go test -tags replay_sqlite ./...
```

The default lightweight mode uses:

- `session/inmemory` and `memory/inmemory`
- `replaytest.JSONSessionService` and `replaytest.JSONMemoryService`
- a deterministic test summarizer, so no API key is required

The standalone SQLite module uses `session/sqlite` and `memory/sqlite`; run it
with `-tags replay_sqlite`. It requires `CGO_ENABLED=1` and a working C compiler
because it depends on `github.com/mattn/go-sqlite3`.

## Optional Integration Mode

Additional persistent backends should be registered in `tests/replay_consistency`
behind environment variables and skipped when unset:

- `REPLAY_REDIS_URL`
- `REPLAY_POSTGRES_DSN`
- `REPLAY_MYSQL_DSN`
- `REPLAY_CLICKHOUSE_DSN`

Backends that do not support event paging, TTL, Track, or a memory query mode
should set `Backend.Capabilities` with `Supported=false`, `AllowedDiff`, and an
explanation. The report keeps these entries as `unsupported` so missing features
are visible without failing lightweight compatibility checks.

## Design

该 harness 用一组标准化 replay operation 驱动不同后端，再读取为统一 Snapshot 比较。归一化时忽略自动生成 event/response ID、后端私有 metadata 和 map/JSON 字段顺序；state、extension、track payload 先按 JSON 解析后重组，浮点 score/duration 保留固定精度。Summary 按 filter-key 分组，严格比较文本、session 归属、boundary version、cutoff 和 last event；last event 会映射为 event#N，避免后端 ID 差异误报。Track 按 track name 分组，比较 event type、关联 invocation、错误和耗时 payload。allowed_diff 只用于声明式能力缺失或容差内浮点指标；summary 丢失、filter-key 错误、覆盖错误和 session 归属错误始终是 blocking diff。后端接入通过公开 session.Service、session.TrackService 和 memory.Service 完成，轻量模式默认 InMemory+SQLite，Redis/Postgres/MySQL/ClickHouse 可用环境变量注册。
