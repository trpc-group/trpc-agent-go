# Session / Memory replay consistency harness

This module replays the same normalized operation stream against multiple
backends and compares their observable snapshots. The lightweight suite uses
`session/inmemory` as the baseline and `session/sqlite` as the persistent
candidate, plus the matching in-memory and SQLite memory services.

## 当前支持的后端

轻量模式**仅接入**以下两个后端，不需要任何外部依赖、服务或环境变量：

| 数据域 | 基准后端 | 候选后端 |
| --- | --- | --- |
| Session | `session/inmemory` | `session/sqlite` |
| Memory | `memory/inmemory` | `memory/sqlite` |

下面 "未来接入约定" 一节列出的 Redis / Postgres / MySQL / ClickHouse
环境变量，**当前代码并未读取**，设置它们不会触发任何回放测试。它们只是
为后续接入预留的命名约定，待相应适配器实现并读取这些变量后才会生效。
在适配器到位之前，相关后端的对比通过在真实后端上运行轻量用例之外的
集成测试覆盖（不在本模块默认 `go test ./...` 路径内）。

## Design notes

**Normalization strategy.** Auto-generated IDs, event timestamps, JSON field
order, and map iteration order are all flattened: memory entries derive a
`semantic-*` id from a sha256 of their content and metadata, so backend-assigned
auto-increment ids are erased; timestamps are reduced to presence/boundary
booleans; JSON is canonically encoded (tool-call `Arguments`, extensions and
track payloads are re-serialized through `tool.NormalizeJSON`), topics and
participants are sorted, and track payloads are compared recursively; vector
similarity and retrieval scores are intentionally excluded from comparison.

**Summary comparison strategy.** Text, filter-key, version, whether timestamps
are set, and the truncation `LastEventID` are compared field-by-field under a
strict equality contract, paired by filter key. This detects, with 100%
coverage, missing summaries, overwrite errors (stale version or wrong text),
wrong session ownership (a candidate gaining or losing a whole summary), and
the Go-specific filter-key error. The fake summarizer derives its output from
the ordered event ids/content and the requested filter key it actually receives,
so a wrong filter-key, a stale event window, or a missing refresh is observable
in the summary text rather than masked by a constant stub.

**Track comparison strategy.** Each payload is compared as a JSON tree, so a
diff can be located to a track name, an intra-track event index, and a concrete
field. `duration_ms` and other latency metrics only drift across backend run
environments, so they are classified as `allowed_diff` and never block.

**allowed_diff rules.** Declared by `DefaultAllowedRules` and matched on domain
plus a field-path suffix; a match is filed separately as `allowed_diff` and does
not affect `Passed`, while any other field mismatch blocks. A backend that lacks
paging, TTL, tracks, or a memory query must emit an `unsupported` record with
`allowed_diff: true` and a reason — it must never silently omit the capability.

**Backend onboarding.** The lightweight mode defaults to InMemory ↔ SQLite and
needs no external dependency.

## Run lightweight mode

```powershell
cd session/replaytest
$env:CGO_ENABLED = "1"
go test ./... -count=1
```

```bash
cd session/replaytest
export CGO_ENABLED=1
go test ./... -count=1
```

The suite contains ten public cases: single turn, multi-turn ordering, state
overwrite/delete/clear, tool calls and responses, memory read/search, summary
creation and replacement, filter-key summaries, summary truncation, tracks,
concurrent append, and recovery after a failed operation. No service or API
key is required; it normally completes in well under 30 seconds.

## Reports and normalization

`compare.CompareSession` and `compare.CompareMemory` return field-level
reports. `compare.WriteReport(path, reports)` writes a JSON artifact; an
example is [`session_memory_summary_track_diff_report.json`](session_memory_summary_track_diff_report.json).
Every diff includes the case, backends, field path, baseline and candidate
values, plus a locator for session/event, state key, summary filter key, track,
or semantic memory id.

Automatic IDs are replaced with a hash of normalized memory content and
metadata. Timestamps are reduced to presence/boundary semantics, JSON objects
are canonically encoded (including tool-call arguments), maps and topics are
sorted, and vector scores are not compared. Track payload is recursively
compared as JSON; `duration_ms` is an allowed difference because it measures
backend execution. Summary text, filter-key, version, cutoff and final event ID
are strict comparisons. This detects missing summaries, incorrect replacement,
wrong session snapshots and filter-key leakage without treating
implementation-specific clock values as failures.

## 未来接入约定（尚未实现）

Redis、Postgres、MySQL 和 ClickHouse **尚未接入**。下列环境变量仅为后续
适配器预留的命名约定，**当前代码不读取它们**，设置这些变量不会运行任何
对应后端的回放测试，也不会改变轻量模式的行为：

```text
REPLAY_REDIS_URL=redis://127.0.0.1:6379
REPLAY_POSTGRES_DSN=postgres://user:pass@127.0.0.1/db
REPLAY_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/db
REPLAY_CLICKHOUSE_DSN=clickhouse://user:pass@127.0.0.1:9000/db
```

接入这些后端时，适配器应：

- 在上述环境变量未设置时跳过对应集成测试；
- 读取该变量连接真实服务，并复用 `compare` / `normalize` / `harness` 逻辑；
- 对暂不支持分页、TTL、track 或某类 memory 查询的能力，必须在报告中以
  `unsupported`（`allowed_diff: true`）记录并说明原因，不得静默省略。

在这些适配器实现并真正读取上述变量之前，本说明中的“未设置则跳过”语义
不成立——当前没有任何后端依赖这些变量。
