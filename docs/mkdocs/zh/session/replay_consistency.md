# 多后端回放一致性

`session/replaytest` 用同一条轨迹驱动多个 Session 与 Memory 后端，捕获公开接口可见的回放状态，并输出语义差异。`test/replay_consistency_test.go` 内置的核心矩阵比较 InMemory 与 SQLite；`test/replay_consistency_external_test.go` 还提供默认运行的 miniredis 模拟器测试和环境变量门控的真实服务测试。

## 运行轻量矩阵

从仓库根目录运行比较器单元测试：

```bash
go test ./session/replaytest -count=1
```

完整本地测试不需要外部服务，包含 InMemory、SQLite 和 miniredis。SQLite 需要 CGO；未配置的真实外部后端会逐项跳过：

```bash
cd test
CGO_ENABLED=1 go test . -run ReplayConsistency -count=1
```

设置 `TRPC_AGENT_REPLAY_REPORT_PATH` 可输出健康矩阵报告：

```bash
cd test
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REPORT_PATH="$PWD/replay-report.json" \
  go test . -run '^TestReplayConsistencyMatrix$' -count=1
```

注入差异示例位于 `session/replaytest/testdata/session_memory_summary_track_diff_report.json`。每条差异包含场景、后端、session ID、精确字段路径、双方值，以及 event 下标、稳定 memory ID、summary filter-key 或 track name 等定位信息。

## 真实回放场景

公开矩阵使用真实 InMemory 与 SQLite 服务运行以下 12 个场景：

1. `single_turn`：一条 user event 和一条 assistant event。
2. `multi_turn`：按顺序追加三轮 user/assistant 对话。
3. `tool_call_and_response`：工具调用、JSON 参数、工具响应和 tool-call-args extension。
4. `state_update_overwrite_delete`：App、User State 删除，以及 Session State 更新、覆盖和显式 null。
5. `session_state_direct_round_trip`：不依赖 Event，直接创建、覆盖并读回 Session State。
6. `memory_search_order_and_score`：检索事实与情景 Memory，并比较 rank 和量化后的 score。
7. `memory_update_and_delete`：持久化更新与删除语义。
8. `summary_filter_and_update`：filter-key Summary 的生成和覆盖更新。
9. `summary_event_window_recovery`：通过 `WithEventNum(2)` 读取 Summary 与保留的两条尾部事件。
10. `track_status_and_error`：按顺序保存 started 与 failed Track 载荷。
11. `concurrent_tool_event_interleaving`：六个 goroutine 同时写入工具事件，再按预置逻辑时间恢复确定顺序。
12. `failure_recovery_without_duplicates`：首次写入失败后真实重试，再模拟确认丢失并以读取结果决定是否重写，检查重复、脏 State 和 Summary 恢复。

每个公开 case 都会在 SQLite 读取后、Normalize 前注入对应后端漂移，完整经过 Load、Capture、Normalize 和 Compare，并要求输出带定位信息的非 allowed diff。另有 21 类细粒度 event、state、memory、summary 与 Track 漂移，以及四类原始 Summary 故障测试。

## 归一化与报告

框架克隆 Session 后再归一化事件。自动生成的 event、invocation 与 tool-call ID 会映射为稳定别名；时间戳、request ID 和响应创建时间会被移除。StateDelta、工具参数与响应、extensions 和 Track payload 均按保留数字精度的 JSON 解码。

Memory 默认保留后端读取顺序，以及 `rank`、六位小数量化后的 `score`、App/User 作用域、内容和元数据，稳定 ID 在归一化后分配。只有场景契约明确不关心顺序时，Case 才可设置 `UnorderedMemories`；此时按语义排序，并将 rank 设为 `-1`。并发事件 case 可显式设置 `OrderEventsByTimestamp`，用输入中的逻辑时间恢复确定顺序。

Summary 快照报告 App、User 和 Session 归属，map 与 boundary filter-key、边界是否存在及版本、正文与主题，以及更新时间、截断时间和 `LastEventID` 对应的 event 下标。Track 快照保留 track name、事件顺序与规范化载荷，只移除配置为易变的 duration、latency 等字段。

`CaseReport.capabilities` 保存各后端完整能力表。不支持的内置 section 不会被捕获或参与语义比较，但必须填写原因。能力健康度以当前 case 为边界：既非当前 case 必需能力、也未导致当前 case 跳过的 unsupported capability 不会使报告失败；被跳过的必需能力必须显式设置 `allowed_diff: true`，否则报告不健康。

每个公开 Case 通过 `RequiredCapabilities` 声明执行所必需的能力。Harness 要求所有后端显式填写这些 capability；漏填会直接报错，而不是沿用默认支持并产生假通过。基准后端必须支持全部必需能力；对比后端若显式 unsupported，则不执行该 case，并把能力名写入 `skipped_backends`。若没有任何候选后端实际完成比较，报告设置 `inconclusive: true`，`HasUnexpectedDiff` 不会把它当成健康结果。像 `event_state_delta_null` 这样仍需运行后观察差异的细粒度语义不列为硬前置能力。

子能力可以描述 section 内的部分语义，而不跳过整个 section。`Event.StateDelta` 中的 nil 表示显式 JSON null（回放 tombstone），不是物理删除；真正删除键由 `Session.DeleteState` 完成。真实 Redis HashIdx 会保存该 null，但 miniredis 的 Lua/cjson 模拟会保留旧值，因此只有 `miniredis` fixture 将 `event_state_delta_null` 标记为 unsupported/allowed。State 继续完整参与比较，只在对应场景中精确允许 `$.state.remove_me` 和 `$.state.pending`；测试还要求两个差异实际出现，避免失效或过宽的白名单。

缺失路径与显式 JSON `null` 不同。报告通过 `baseline_present` 和 `compared_present` 标记存在性：缺失侧写为 `{"missing": true}`，存在的 null 仍写为 `null`。

`AllowedDiff` 必须指定双方后端、一个 section、一个精确路径和原因。通配符会被拒绝，路径也必须属于声明的 section。例如：

```go
replaytest.AllowedDiff{
	Section:  "tracks",
	Path:     "$.tracks.tool[0].payload.backend_note",
	BackendA: "inmemory",
	BackendB: "sqlite",
	Reason:   "SQLite exposes a backend-only diagnostic note",
}
```

## 可选外部后端

`TestReplayConsistencyExternalBackends` 已接入 Redis、PostgreSQL、MySQL 和 ClickHouse。每个子测试只读取自己的变量；未设置时跳过，设置后按同一组 12 个场景运行。满足必需能力的场景与独立 InMemory 基线比较；其余场景明确记录为 inconclusive/skip：

| 后端 | 设置后启用 | 示例 | 服务接线方式 |
| --- | --- | --- | --- |
| Redis | `TRPC_AGENT_REPLAY_REDIS_URL` | `redis://localhost:6379/15` | `session/redis.WithRedisClientURL` 与 `memory/redis.WithRedisClientURL` |
| PostgreSQL | `TRPC_AGENT_REPLAY_POSTGRES_DSN` | `postgres://user:pass@localhost:5432/replay?sslmode=disable` | `session/postgres.WithPostgresClientDSN` 与 `memory/postgres.WithPostgresClientDSN` |
| MySQL | `TRPC_AGENT_REPLAY_MYSQL_DSN` | `user:pass@tcp(localhost:3306)/replay?parseTime=true` | `session/mysql.WithMySQLClientDSN` 与 `memory/mysql.WithMySQLClientDSN` |
| ClickHouse | `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | `clickhouse://user:pass@localhost:9000/replay` | `session/clickhouse.WithClickHouseDSN`；报告中的组合后端名为 `clickhouse-session+inmemory-memory`，Events、Summary、Track 标记为 unsupported/allowed |

例如只运行真实 Redis 集成：

```bash
cd test
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REDIS_URL='redis://localhost:6379/15' \
  go test . -run '^TestReplayConsistencyExternalBackends/redis$' -count=1 -v
```

PostgreSQL、MySQL 和 ClickHouse 使用对应表格变量和相同的测试名后缀。服务构造时配置相同的确定性 summarizer、Summary filter-key 策略和固定表前缀；每个 case 使用唯一 AppName。PostgreSQL 与 MySQL 关闭软删除并在结束后物理清理 Session、Memory、App State 与 User State。当前 PostgreSQL 时间列为无时区类型，运行 Summary 集成时应设置 `TZ=UTC`；否则本地时间与 UTC Summary boundary 会被读成相差一个时区。ClickHouse 使用唯一命名空间隔离 tombstone 数据；其 25.3 JSON schema 无法无损保存 dotted extension key、显式 null 和 Summary String 扫描，因此运行直接 State 与 Memory 场景，其余场景明确 SKIP。外部服务应使用专用测试数据库和建表权限；日志不会主动输出 DSN。

## 设计说明

框架克隆 Session 后，将事件、StateDelta、工具参数、扩展和 Track 载荷解码为保留数字精度的 JSON，并把自动 ID 映射为稳定别名。Memory 默认保留顺序、rank、量化 score 和作用域，仅在 Case 声明无序时排序。Summary 比较归属、filter-key、边界版本和 event 下标；Track 保留名称、顺序及载荷，只移除耗时字段。allowed_diff 必须指定后端、section、精确路径和原因，并区分缺失与 null。能力表可跳过完整 section，也可记录部分语义并继续比较；外部服务未配置时逐项跳过。
