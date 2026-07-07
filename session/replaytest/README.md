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

The public cases are returned by `PublicCases()` and cover:

1. single-turn dialogue
2. multi-turn event ordering
3. tool call, tool response, and tool-call-args extension
4. state set, overwrite, delete, and clear semantics
5. memory write/read
6. summary generation and filter-key update
7. summary plus retained post-compression events
8. track events with duration/error payloads
9. interleaved sub-agent/tool writes
10. retry/failure recovery without duplicate replay output

## Optional Integration Backends

The default package intentionally avoids importing nested modules such as
`session/redis`, `session/postgres`, `session/mysql`, and `session/clickhouse`,
because those are independent Go modules. Integration tests for those backends
should live in the backend module and reuse this package by calling `PublicCases`,
`Run`, and `CompareSnapshots`.

Recommended environment gates:

```bash
(cd session/redis && TRPC_REPLAY_REDIS_DSN=redis://127.0.0.1:6379/0 go test ./...)
(cd session/postgres && TRPC_REPLAY_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/db?sslmode=disable' go test ./...)
(cd session/mysql && TRPC_REPLAY_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/db?parseTime=true' go test ./...)
(cd session/clickhouse && TRPC_REPLAY_CLICKHOUSE_DSN='clickhouse://127.0.0.1:9000/db' go test ./...)
```

When a backend does not support event pages, TTL, tracks, or memory search, its
adapter should add an `UnsupportedFeature` with `allowed_diff=true` and a concrete
explanation. Unsupported features are reported but do not hide mismatches in
supported fields.

## Design Notes

该框架将“写入轨迹”和“读取比较”分离：replay case 用标准操作描述事件、state、memory、summary 和 track，后端适配器负责写入真实或轻量持久化实现；比较器只处理读取后的规范化 snapshot。归一化会去除自动 event id、响应 id、时间戳、JSON/map 顺序和后端私有 metadata；memory 使用内容、scope、topics、kind 生成稳定 id，并把相似度归入区间。summary 按 filter-key 排序，比较文本、boundary version、session 归属、覆盖后的 cutoff event 引用和更新时间是否存在，从而检测丢失、覆盖错误、归属错误和 filter-key 错误。track 按 track name 和追加顺序比较，payload 先规范化 JSON，duration/elapsed/latency 等耗时字段替换为稳定占位，错误信息和 invocation 保留。allowed_diff 只用于明确 unsupported 的能力，例如某后端没有事件分页或 TTL；任何已支持字段差异都会生成包含 case、backend、session id、field path、summary filter-key、memory id 或 track name 的阻断 diff。
