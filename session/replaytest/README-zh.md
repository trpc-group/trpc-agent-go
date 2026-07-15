# 回放测试 — 多后端回放一致性测试框架

一个确定性的、基于快照的测试框架，对多个 Session/Memory 后端执行相同操作序列并比对每个中间状态。为生产级稳定性设计，支持原子写入、熔断器、并行执行和校验和验证报告。

## 设计说明

本框架将所有后端数据归一化为纯 JSON `Snapshot` 后再比较，与 Go 结构体布局解耦。
- 归一化策略包括：
    - 通过 `IDAliasMap` 将 UUID 替换为稳定别名（如 `event-000`、`tool-call-001`）以保留交叉引用关系；
    - 将 StateDelta 中 `nil` 值转换为 `MissingValue{}`（序列化为 `{"__missing":true}`），与显式 `null` 区分，精确表达"字段缺失"与"字段存在但值为 null"的语义差异；
    - 移除 `duration`、`duration_ms`、`elapsed`、`elapsed_ms`、`latency`、`latency_ms` 等易变键；按内容排序 Memory（`UnorderedMemories`）和按 JSON 表示排序 Track 事件。
- Summary 比较策略将 `UpdatedAt`、`CutoffAt`、`LastEventID` 转换为事件索引进行精确对比，`FilterKey` 要求精确匹配，`Text` 允许通过 `AllowedDiff` 声明已知截断差异。
- Track 比较策略对每个 Track 名称下的事件序列逐个比较 Payload，自动剥离易变键，按 JSON 排序消除顺序差异。`AllowedDiff` 规则要求精确的 section+path 匹配（禁止通配符）、必须填写原因、双向匹配、强制 section-path 一致性。
- 后端通过环境变量接入：轻量模式仅需 InMemory+SQLite（CGO），Redis/Postgres/MySQL/ClickHouse 通过对应 DSN 环境变量可选启用。

## 变更内容

- 新增可复用的 `session/replaytest`：统一 Capture、Normalize、Compare、精确 allowed diff 与原子 JSON 报告写入
- 提供 15 个公开 replay case，覆盖普通/多轮/工具对话、State、Memory、Summary、Track、StateDelta null 语义、Scope 状态、Summary filter-key、真实并发和真实失败重试
- 接入 InMemory + SQLite 轻量矩阵，以及按环境变量启用的 Redis、PostgreSQL、MySQL、ClickHouse 集成测试
- 区分缺失值与显式 `null`，保留 event index、memory ID、summary filter-key、track name 等精确 locator
- 引入 `RequiredCapabilities`、`SkippedBackends` 与 `inconclusive`，防止未执行比较或未声明能力被误判为健康
- 增加中英文运行文档、导航和 `session_memory_summary_track_diff_report.json` 示例

修复 #2001

## 项目结构

```text
session/replaytest/
├── case.go             # Case、Op 类型定义
├── normalize.go        # 快照归一化（ID 别名、MissingValue、易变键）
├── diff.go             # 快照优先的差异比较引擎，含严重级别分类
├── harness.go          # Harness（Run、RunSuite）、Capture、报告 I/O、重试、检查点
├── factory.go          # BackendFactory + InMemory / SQLite / miniredis / 外部工厂
├── golden.go           # Golden Trace 保存/加载/回归检测
├── types.go            # 共享类型（Snapshot、Backend、Capabilities、RetryPolicy、Report 等）
├── cases_test.go       # 15 个回放用例 + 漂移检测 + 摘要故障 + 报告测试
├── helpers_test.go     # 测试辅助工具（事件构建器、断言、makeBackends）
├── unit_test.go        # 所有组件的单元测试
├── README-zh.md        # 本文件（中文，含设计说明）
├── README-en.md        # 英文版（含设计说明）
└── go.mod              # 独立 Go 模块（导入 session/sqlite）
```

## 快速开始

所有命令应从 **`session/replaytest`** 目录运行（该目录有独立的 `go.mod`）。SQLite 后端需要 `CGO_ENABLED=1`。

### 轻量模式（默认）

仅使用 InMemory + SQLite，无需外部依赖，运行时间 <3 秒：

```bash
cd session/replaytest
# Linux/macOS：
CGO_ENABLED=1 go test . -v
# Windows PowerShell：
$env:CGO_ENABLED="1"; go test . -v
```

### 自我验证模式

InMemory vs InMemory（无需 CGO）：

```bash
cd session/replaytest
go test . -v -run TestReplay_Smoke_InMemorySelfVerify
```

### 运行单个用例

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -v -run "TestReplay_All/case01"
```

### 生成差异报告

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -v -run TestReplay_Report
```

### 竞态检测

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -race -v
```

## 后端接入说明

### 轻量模式

默认无需任何外部服务。框架内置 InMemory 和 SQLite 后端：

- **InMemory**：纯内存实现，零依赖
- **SQLite**：需要 `CGO_ENABLED=1` 和 C 编译器

### 集成模式（外部后端）

通过设置环境变量启用外部数据库后端。框架会自动执行健康检查（Probe）、预热（WarmUp）和泄漏检测（VerifyCleanup）：

| 环境变量 | 后端 | 说明 |
|----------|------|------|
| `TRPC_AGENT_REPLAY_REDIS_URL` | Redis | 为空时自动使用 miniredis（内置模拟） |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | PostgreSQL | 完整连接字符串，如 `postgres://user:pass@localhost:5432/test` |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | MySQL | DSN 格式，如 `user:pass@tcp(localhost:3306)/test` |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | ClickHouse | ClickHouse 连接字符串 |

**示例：启用 Redis 集成测试**

```bash
# 使用本地 Redis
$env:TRPC_AGENT_REPLAY_REDIS_URL="redis://localhost:6379"; $env:CGO_ENABLED="1"; go test ./session/replaytest/ -v -run TestReplay_Smoke

# 使用内置 miniredis（无需 Redis 服务）
$env:CGO_ENABLED="1"; go test ./session/replaytest/ -v -run TestReplay_Smoke
```

**示例：启用全后端集成测试**

```bash
export TRPC_AGENT_REPLAY_REDIS_URL="redis://localhost:6379"
export TRPC_AGENT_REPLAY_POSTGRES_DSN="postgres://localhost:5432/test"
export TRPC_AGENT_REPLAY_MYSQL_DSN="root@tcp(localhost:3306)/test"
export TRPC_AGENT_REPLAY_CLICKHOUSE_DSN="clickhouse://localhost:9000"
CGO_ENABLED=1 go test ./session/replaytest/ -v -run TestReplay_All
```

### 其他环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `REPLAY_BACKEND` | `sqlite` | 目标后端：`inmemory` / `sqlite` |
| `TRPC_AGENT_REPLAY_REPORT_PATH` | （空） | RunSuite 后差异报告写入路径 |

## 15 个回放用例

| # | 用例 | 必需能力 | 覆盖范围 |
|---|------|----------|----------|
| 1 | 单轮对话 | events | 创建 + 2 个事件 + GetSession |
| 2 | 多轮对话 | events | 10 个事件序列完整性 |
| 3 | 工具调用交叉引用 | events | ToolCalls、ToolCallID、Extensions、invocation ID 别名 |
| 4 | 状态更新 | events, state | 创建带状态、键值、覆盖、通过 nil 删除、通过事件的 StateDelta |
| 5 | Memory 搜索 | memory | AddMemory、ReadMemories、分数、元数据、无序比较 |
| 6 | 摘要过滤 | summary | CreateSessionSummary（主摘要 + 分支摘要）、GetSummaryText |
| 7 | 摘要窗口 | summary | 20 个事件 + 摘要 + 5 个新事件、边界索引（允许差异） |
| 8 | Track 事件 | track | start/end/error 负载，易变键已移除（`duration`、`latency_ms` 等） |
| 9 | 事件计数 | events | 15 个事件（3×5）、CountOnly 模式用于跨后端计数验证 |
| 10 | 故障恢复 | events, summary | 重复事件追加、状态覆盖、幂等摘要，无损坏 |
| 11 | StateDelta null | events, state | nil StateDelta → MissingValue、CapEventStateDeltaNull |
| 12 | 边界与错误 | events, state | 空状态、extensions、branch/tag/filterKey、过去的 EventTime、大 EventNum |
| 13 | 状态删除 | state | 创建带状态的会话，通过设置 nil 删除键 |
| 14 | 状态作用域 | state | AppState（应用级）和 UserState（用户级）跨会话共享 |
| 15 | 摘要 filterKey | summary, events | 带 filterKey 的事件、摘要限定到特定 filterKey |

## 能力声明

| 能力 | 说明 |
|------|------|
| `events` | 能存储和检索 Session 事件 |
| `state` | 能存储和检索 Session 状态 |
| `memory` | 能存储和检索 Memory 条目 |
| `summary` | 能创建和检索 Session 摘要 |
| `track` | 能追加和检索 Track 事件 |
| `event_state_delta_null` | 支持 StateDelta 中的 nil 值（区分删除和设置为 null） |

当后端缺少必需的能力时，该 Case 将被跳过。如果剩余有效后端少于两个，结果为 `inconclusive` 而非 `pass`，防止因数据不足导致的误报。

## 差异严重级别

| 严重级别 | 条件 | 示例 |
|----------|------|------|
| `critical` | 数据丢失或缺失段落 | MissingValue vs 值，一个后端缺失事件 |
| `major` | 值不匹配 | 内容不同，键错误 |
| `minor` | 允许的差异 | 已知的架构差异，有文档说明的原因 |

## 报告格式

示例报告：`test/session_memory_summary_track_diff_report.json`

该示例报告由 `TestReplay_ReportWithDiffs` 测试使用**真实后端执行数据 + 注入差异**生成，每个 diff 都有测试断言验证。报告内容包含：

| Diff 类型 | Case | 路径 | 基准值 (value_a) | 对比值 (value_b) | allowed | 严重级别 | 解释 |
|-----------|------|------|------------------|------------------|---------|----------|------|
| 状态覆盖丢失 | case04 | `$.state.k1` | `"v1-new"` | `"v1"` | false | major | 状态覆盖未生效 |
| 摘要文本截断 | case06 | `$.summaries[""].text` | `"summary-of-10-events"` | `"summary-of-10-events-truncated"` | true | minor | 摘要文本因截断差异 |
| Track 字段缺失 | case08 | `$.tracks["agent-run"][1].payload.status` | `"ok"` | `{"__missing":true}` | false | critical | Track payload 字段在后端中缺失 |
| StateDelta 语义差异 | case11 | `$.events[1].stateDelta.k2` | `{"__missing":true}` | `null` | false | critical | MissingValue（字段缺失）vs nil（显式 null）|
| 后端能力不足 | case12 | — | — | — | — | — | SQLite 不支持 summary/track |

关键字段：

- `report_id`：版本戳（`replay-v2`），替代时间戳
- `version`：Schema 版本（`"v2"`）
- `run_id`：时间戳-pid-主机名，用于 CI 去重
- `severity`：每个差异的分类（critical/major/minor）
- `backend_metrics`：每个 Case 的计时数据和重试指标
- `skipped_backends`：后端 → 不支持能力列表的映射
- `inconclusive_cases`：有效后端不足的 Case 数量
- 校验和伴生文件：`<report>.sha256` 文件与 JSON 报告并存，用于完整性验证

`ReadReportWithVerify` 会从伴生文件重新计算校验和并拒绝损坏的文件。报告文件本身是合法 JSON。版本守卫会拒绝未知的 Schema 版本。

## 验收结果

| 项目                       | 结果                                                                    |
| ------------------------ | --------------------------------------------------------------------- |
| 正常轻量矩阵                   | 15/15 PASS，0 diff，0 inconclusive                                      |
| 公开 case 注入漂移             | 15/15 检出，并断言 section + path + locator                                 |
| 细粒度漂移                    | 4 类 Event/State/Memory/Summary/Track + 4 类原始 Summary 故障全部检出          |
| 轻量模式耗时                   | `real 8.09s`（含 race 检测），低于 30 秒要求                                                |
| `session/replaytest` 覆盖率 | 68.8%                                                                 |
| Race 检测                    | 0 data race，全部并发场景通过                                                |
| go vet                       | 0 warnings（仅 helpers_test.go 中 session.Session 持有锁的拷贝为既有包级问题） |

### 验收标准逐项验证

| 验收标准 | 结果 | 说明 |
| --- | --- | --- |
| 至少支持 InMemory 与一个持久化后端对比 | ✅ | InMemory + SQLite（`:memory:`）默认配对；环境变量可启用 Redis/PostgreSQL/MySQL/ClickHouse |
| 10 条公开 replay case 100% 检出人为注入不一致 | ✅ | 15 条 case 全部 PASS；`TestReplay_ConsistencyDetectsInjectedDrift` 验证 events/state/summaries/tracks 四类注入漂移全部检出 |
| 正常 case 误报率 ≤ 5% | ✅ | 15/15 正常 case 零 diff，误报率 0% |
| summary 丢失/覆盖错误/归属 session 错误检出率 100%，Go 还需覆盖 filter-key 错误 | ✅ | `TestReplay_SummaryFaultsDetected` 验证 summary_lost、summary_text_wrong、summary_filter_key_wrong、summary_boundary_mismatch 四类全部检出 |
| 差异报告能定位 session id/event index/summary id/filter-key/字段路径/两个后端的值；Go 还需支持 track name/memory id | ✅ | Diff 结构包含 Case/SessionID/EventIndex/MemoryID/TrackName/SummaryKey/Path/ValueA/ValueB |
| 轻量模式完整运行耗时 ≤ 30 秒 | ✅ | `real 8.09s`（含 race detector），远低于 30 秒阈值 |

## BackendFactory 接口

```go
type BackendFactory interface {
    Kind() string
    Capabilities() Capabilities
    Create(ctx context.Context, t *testing.T) *Backend
}
```

要添加新的后端，实现该接口并在 `ResolveBackends` 或 `ResolvePair` 中注册。
