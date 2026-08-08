# Replay Consistency Test Framework

多后端 session / memory / summary / track 回放一致性测试框架。

它把同一组确定性输入（`ReplayCase`）驱动到多个后端实现（`Backend`），把各后端的
结果归一化成 backend 无关的 `Snapshot`，再用 `CompareSnapshots` 两两对比并产出结构化的
差异报告（`DiffReport` → JSON），用于发现"同一输入在不同后端上行为不一致"的回归。

## 核心概念

- **`Backend`**：一个 session service + 一个 memory service 的组合，可选 `Setup`/`Teardown`
  生命周期钩子。当前内置两种后端：
  - `inmemory`：`session/inmemory` + `memory/inmemory`
  - `persistent`（文件持久化的"模拟持久化"后端）：`session/replaytest/persistent_backend.go`
    把 session 与 memory 以 JSON 落到磁盘（每次写原子 `tmp+rename`），因此**新建一个指向
    同一目录的实例能看到之前实例写入的数据**——这就是验收标准 #1 要求的"持久化（或模拟持久化）后端"。
- **`ReplayCase`**：backend 无关的确定性输入（事件序列、memory 写/查、summary 步骤、track 事件）。
- **`normalizer.go`**：把各后端结果归一化，剥掉 ID/时间戳/排序/后端私有 metadata 等噪声。
- **`comparator.go`**：快照深度比较，支持 `allowed_diff`（backend 对 + 段落 + 路径通配符），
  并区分"state 键缺失 vs 值为空"、memory/track 按内容 one-to-one 配对。
- **`fault.go`**：确定性故障注入（丢 memory/summary/track/event、覆盖 summary、错 filter-key、
  错 session 归属、篡改 state），用于验证 comparator 能 100% 检出人为注入的不一致。

## 用法

```go
backends := []Backend{
    inmemoryBackend(),
    newPersistentBackend(t.TempDir(), "persistent"),
}
reports, err := RunReplayMatrix(ctx, backends, replayCases(), nil)
```

运行全部测试：

```bash
go test ./session/replaytest/ -count=1
```

生成样例差异报告（`testdata/session_memory_summary_track_diff_report.json`）：

```bash
# 见 TestPersistentBackendCrossBackendComparison：矩阵会自动产出跨后端 DiffReport
```

默认矩阵是 in-memory 双实例；需要把文件持久化后端拉进矩阵做跨后端对比时，用环境变量
选择后端（逗号分隔，按名称过滤）：

```bash
REPLAYTEST_BACKENDS=inmemory,persistent go test ./session/replaytest/ -count=1 -run TestInMemorySessionReplayEventsStateAndMemoryMatch
```

跨后端对比会如实暴露不同后端实现间的真实语义差异（例如检索后端对短查询的匹配行为不同），
这正是框架要发现的回归。

## 验收标准对照

| 验收标准 | 实现 |
|---------|------|
| InMemory + 一个持久化/模拟持久化后端对比 | `standardBackends` 含 in-memory；`persistent_backend.go` 提供文件持久化后端；`TestPersistentBackendRunsAllCases` / `TestPersistentBackendCrossBackendComparison` |
| 10 条公开 case 100% 检出注入不一致 | `TestFaultInjectionDetection`：每个 case × 每种故障类型，断言 comparator 必报 diff |
| 正常 case 误报率 ≤ 5% | 相同 in-memory 后端对比零 unallowed diff（`TestInMemorySessionReplayEventsStateAndMemoryMatch`）；比较器只在真实差异上报 |
| summary 丢失/覆盖/错 session/错 filter-key 检出率 100% | `FaultDropSummary` / `FaultOverwriteSummary` / `FaultWrongSummarySession` / `FaultWrongSummaryFilter` 全覆盖 |
| 差异报告定位到 session id / event index / summary filter-key / 字段路径 / memory id / track name | `FieldDiff` 字段完整（SessionID/EventIndex/MemoryID/SummaryID/TrackName/FieldPath）；样例见 `testdata/session_memory_summary_track_diff_report.json` |
| 轻量模式 ≤ 30 秒 | 纯内存+文件后端，全量测试 < 2 秒 |

## 如何接入新后端

1. 实现 `session.Service`（+可选 `session.TrackService`）与 `memory.Service`。
2. 组装成 `Backend`（含 `Setup`/`Teardown` 生命周期，数据库后端应在此建表/清理）。
3. 加入 `standardBackends` 或直接传入 `RunReplayMatrix`，跑矩阵并查看 diff。

## 目录

- `types.go` / `normalizer.go` / `comparator.go` / `runner.go` / `report.go` / `fault.go` / `persistent_backend.go`
- `replaytest_test.go`：10 条 case + 单元测试 + 验收测试
- `testdata/session_memory_summary_track_diff_report.json`：样例差异报告
