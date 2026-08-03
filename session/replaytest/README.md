# Session / Memory / Summary / Track Replay Consistency

`session/replaytest` provides a deterministic replay harness for comparing Session,
Memory, Summary, and Track behavior across backends. The root-module lightweight
suite runs against:

- `session/inmemory + memory/inmemory`
- `session/jsonfile + memory/jsonfile`, a local JSON-file persistent backend used
  as the SQLite-equivalent lightweight backend so `go test ./...` does not need
  to import nested backend modules.

Run the lightweight suite:

```bash
go test ./session/replaytest
```

Run the real SQLite integration suite from the repository test module:

```bash
(cd test && go test ./... -run TestReplayConsistencySQLiteBackend)
```

Run the real Redis integration suite. It uses `miniredis` by default and does
not require an external Redis server; set `TRPC_REPLAY_REDIS_DSN` to target an
external Redis instance:

```bash
(cd test && go test ./... -run TestReplayConsistencyRedisBackend)
(cd test && TRPC_REPLAY_REDIS_DSN=redis://127.0.0.1:6379/0 go test ./... -run TestReplayConsistencyRedisBackend)
```

The 21 public cases are returned by `PublicCases()` and cover:

1. single-turn dialogue
2. multi-turn event ordering
3. tool call, tool response, and tool-call-args extension
4. state set and overwrite semantics
5. memory write/read
6. summary generation and filter-key update
7. summary plus a retained post-compression event window
8. track events with duration/error payloads
9. concurrent/interleaved sub-agent and tool writes
10. retry/failure recovery without duplicate replay output
11. state delete and clear semantics
12. standalone fact memory write/read
13. duplicate event detection for retry recovery
14. repeated idempotent memory store
15. partial event not persisted
16. event metadata, state delta, tag, branch, and extension roundtrip
17. multi-part-style event with text, tool call, tool response, and parts extension
18. memory written after summary truncation
19. multi-result memory recall ordering
20. memory update, delete, clear, and post-clear recall lifecycle
21. session TTL expiration probe

## Optional Integration Backends

The default package intentionally avoids importing nested modules such as
`session/redis`, `session/postgres`, `session/mysql`, and `session/clickhouse`,
because those are independent Go modules. Integration tests for those backends
should live in the backend module or the repository `test` integration module
and reuse this package by calling `PublicCases`, `Run`, `CompareSnapshots`, or
`NewServiceBackend` with concrete
`session.Service`, `memory.Service`, and optional `session.TrackService`
instances.

Optional SQL integrations are compiled in the repository test module and skipped
unless the matching environment variable is set:

```bash
(cd test && TRPC_REPLAY_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/db?sslmode=disable' go test ./... -run TestReplayConsistencyPostgresBackend)
(cd test && TRPC_REPLAY_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/db?parseTime=true&charset=utf8mb4' go test ./... -run TestReplayConsistencyMySQLBackend)
(cd test && TRPC_REPLAY_CLICKHOUSE_DSN='clickhouse://127.0.0.1:9000/db' go test ./... -run TestReplayConsistencyClickHouseSessionBackend)
```

Postgres and MySQL run full `session/* + memory/*` replay coverage. ClickHouse
currently has a session backend in this repository but no `memory/clickhouse`
module and no `session.TrackService` implementation, so the optional
ClickHouse test uses `session/clickhouse + memory/inmemory` and reports Track as
`unsupported` with `allowed_diff=true`.

The harness probes event-page reads with `WithGetSessionEventPage`; cases may
also set a read event limit to model summary-based context compression where
only retained post-summary events are replayed alongside the saved summary. The
TTL case requires backends that declare `CapabilityTTL` to create a fresh
short-lived session service, wait for expiry, and verify `GetSession` no longer
returns that session. When a backend does not support event pages, TTL, tracks,
memory search, or session-state delete/clear semantics, its adapter should add
an `UnsupportedFeature` with `allowed_diff=true` and a concrete explanation.
Unsupported features are reported; supported-field mismatches still block the
run, while diffs tied to an explicitly unsupported capability are retained in
the report as allowed diffs. The generic
`session.Service` API exposes merge-only `UpdateSessionState`, so service-backed
adapters mark state delete/clear unsupported unless their adapter supplies a
native deletion path. The lightweight JSONFile backend and the repository
SQLite integration adapter both exercise delete/clear as supported
capabilities.

## Design Notes

框架把标准 replay 操作写入每个后端，再把读回结果转换成稳定 snapshot。归一化保留确定性的 event id、memory logical id 和原始 event_order，同时规范 JSON/map 顺序、固定 replay 时间、私有 metadata、memory score 与 track duration；并发 case 既按 deterministic timestamp 得到可比较事件序列，也单独比较 raw order。summary 按 filter-key、文本、boundary/version、cutoff、更新时间、期望归属 session 和 deterministic owner/filter marker 比较，并从 replay 操作推导写入时 cutoff，截断窗口外事件归一化为 event[missing] 加 cutoff 时间，用来发现丢失、覆盖、跨 session 或 filter-key 错误。memory 从 replay 操作推导最终集合，单后端即可发现重复、泄漏、删除或 clear 失败。track 保留 name、type、invocation、错误 payload 和时间序列，只折叠耗时类数值。TTL 通过短生命周期 session 的真实过期读回验证；不支持 TTL 的后端在报告中标为 allowed_diff。allowed_diff 只能由 backend adapter 明确声明 unsupported 能力并给出说明；否则任何字段差异都会阻塞。轻量模式使用 InMemory 与 JSONFile，真实 SQLite 通过 test 模块接入并支持 state delete/clear；Redis/Postgres/MySQL 因通用 session.Service 仅暴露 merge-only state 更新，仍明确报告为 unsupported allowed_diff，除非适配器提供原生删除路径。
