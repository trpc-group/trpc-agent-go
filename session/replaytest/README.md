# Replay Consistency Harness

`session/replaytest` provides a backend-neutral replay harness for session,
memory, summary, and track consistency checks. The default lightweight test
compares InMemory with a JSON file-backed local persistent simulator without modifying
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

The standalone module also has a default JSON local-persistence test. Its SQLite
test uses `session/sqlite` and `memory/sqlite`; run it with `-tags replay_sqlite`.
It requires `CGO_ENABLED=1` and a working C compiler because it depends on
`github.com/mattn/go-sqlite3`.

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

该 harness 用标准 replay operation 驱动多个后端，再读取为统一 Snapshot。归一化会稳定 JSON/map 顺序，隐藏自动 event/response ID 和后端私有 metadata，把 memory id 映射为逻辑 ID；score 与 duration 只允许容差差异。Summary 按 filter-key 比较文本、版本、boundary、更新时间与持久化 session 归属，丢失、错 key、错 owner 或覆盖成旧内容均为 blocking diff。Track 按 track name 保留时间序列、event type、invocation、error 与耗时。后端只需实现 session.Service、可选 session.TrackService 和 memory.Service；轻量模式为 InMemory+JSON，本地/外部集成由环境变量启用。
