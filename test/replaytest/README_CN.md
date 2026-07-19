# Session / Memory 多后端回放一致性测试框架

## 设计概述

`replaytest` 是 tRPC-Agent-Go Session 与 Memory 服务的多后端回放一致性测试框架。它通过 JSON 场景定义相同的操作序列，驱动多个后端执行，采集归一化快照，生成字段级差异报告以检测跨后端不一致。

**设计动机**：不同 Session / Memory 后端（InMemory、SQLite、Redis、Postgres、MySQL、ClickHouse）在处理相同 Agent 轨迹时必须行为一致。事件顺序、状态值、记忆内容或摘要元数据的任何差异都会导致回放损坏。本框架：

1. **采集** 在每个后端回放相同操作序列（事件、状态更新、记忆增删改、摘要、轨道事件）后，生成 `ReplaySnapshot` 快照。
2. **归一化** 剥离自动生成字段（ID、时间戳、JSON 序、map 迭代序），使快照可比较。
3. **比较** 使用递归深度对比，按截面（session、events、state、memory、summary、tracks）逐步比较两份快照。
4. **报告** 字段级差异，含路径、值和上下文（事件索引、记忆 ID、摘要 filter-key、轨道名）。

**归一化策略**：剥离事件和响应中的 `id`、`timestamp`、`created` 字段。解码 JSON 编码的 state 值。按确定性 key 排序记忆条目，按名称排序轨道事件。所有时间戳统一为 UTC RFC 3339。通过 marshal→unmarshal 规范化 JSON 值以消除 map/slice 顺序差异。

**白名单差异规则**：预期的跨后端差异（如并发事件顺序不确定、SQLite 摘要序列化差异）可通过通配符路径模式在 JSON 场景文件中标记为 `allowed_diffs`，并附解释说明。

## 快速开始

```bash
# 运行轻量模式（InMemory vs SQLite，约 110ms，零外部依赖）
cd test && go test ./replaytest/... -v

# 运行指定用例
go test ./replaytest/... -run TestReplayConsistency_AllCases -v
```

## 轻量模式

对比 **InMemory** 与 **SQLite**，使用 `t.TempDir()` 创建的临时数据库。无需任何外部服务，运行时间约 110ms。这是默认且始终开启的模式。

验收目标：完整 10 条用例套件耗时 ≤ 30 秒。

## 集成模式

可通过环境变量开启额外后端（未来增强）：

| 变量 | 后端 |
|----------|---------|
| `TRPC_AGENT_REPLAY_REDIS_ADDR` | Redis |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | PostgreSQL |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | MySQL |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | ClickHouse |
| `TRPC_AGENT_REPLAY_REPORT_PATH` | 自定义差异报告输出路径 |

## Replay Case JSON 格式

每条 replay case 是一个 JSON 文件，结构如下：

```json
{
  "name": "case_name",
  "description": "用例描述",
  "app_name": "my-app",
  "user_id": "user-id",
  "session_id": "session-id",
  "steps": [...],
  "verify": {"events_count": 5},
  "allowed_diffs": [...]
}
```

### Step 类型

| 类型 | 说明 | 关联字段 |
|------|------|---------|
| `create_session` | 创建新 session，可选初始状态 | `state` |
| `append_event` | 向 session 追加事件 | `event` |
| `update_app_state` | 更新应用级状态 | `state` |
| `update_user_state` | 更新用户级状态 | `state` |
| `update_session_state` | 更新会话级状态 | `state` |
| `add_memory` | 新增记忆条目 | `memory` |
| `update_memory` | 通过别名更新记忆 | `memory` |
| `delete_memory` | 通过别名删除记忆 | `memory` |
| `create_summary` | 创建或更新会话摘要 | `summary` |
| `append_track` | 追加轨道事件 | `track` |
| `concurrent_events` | 在并行 goroutine 中执行子步骤 | `concurrent` |
| `get_session` | 验证快照点 | — |

### Event 字段说明

`author`、`role`、`content`、`tool_calls`（`{id, name, arguments}` 数组）、`tool_id`、`tool_name`、`branch`、`filter_key`、`tag`、`state_delta`、`extensions`、`actions`（`{skip_summarization}`）

**重要**：每个 session 必须包含至少一条用户消息（`role: "user"`），因为 session 事件过滤需要用户消息作为锚点。

### Memory 字段说明

`op`（"add" / "update" / "delete"）、`ref`（更新/删除时的别名引用）、`result_alias`（存储记忆 ID 以便后续引用）、`content`、`topics`、`metadata`（`{kind, event_time, participants, location}`）。

### Summary 字段说明

`filter_key`（摘要覆盖的事件 filter key）、`text`（确定性摘要文本，无需 LLM）、`force`（强制重新生成）。

### Track 字段说明

`name`（轨道标识符）、`payload`（任意 JSON 对象）。

## 差异报告格式

差异报告是一个 `DiffEntry` 对象的 JSON 数组：

```json
{
  "case": "case_name",
  "session_id": "session-id",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "section": "events",
  "path": "$.events[0].content",
  "left": "后端 A 的值",
  "right": "后端 B 的值",
  "allowed": false,
  "reason": "",
  "context": {
    "event_index": 0
  }
}
```

- **path**：类 JSONPath 的差异字段路径
- **context**：定位信息（event_index、memory_id、summary_filter_key、track_name、track_event_index）
- **allowed**：该差异是否匹配 `allowed_diffs` 规则
- **reason**：匹配的 allowed-diff 规则说明

### Context 定位字段

| 截面 | Context 字段 |
|---------|---------------|
| `events` | `event_index` |
| `memory` | `left_memory_key`、`left_memory_id`、`right_memory_key`、`right_memory_id` |
| `summary` | `summary_filter_key` |
| `tracks` | `track_name`、`track_event_index` |

## 功能覆盖

轻量模式的两个后端（InMemory、SQLite）对 replay 框架覆盖的所有操作均完整支持，无跨后端功能差异。

| 操作 | InMemory | SQLite |
|-----------|:--------:|:------:|
| Session 创建 / 事件追加 | ✅ | ✅ |
| State 更新（app / user / session） | ✅ | ✅ |
| Memory 增删改查 | ✅ | ✅ |
| Session 摘要生成 | ✅ | ✅ |
| Track 事件追加 | ✅ | ✅ |
| 并发事件写入 | ✅ | ✅ |

集成后端（Redis、Postgres、MySQL、ClickHouse）接入后如有功能差异，应在此处记录并在对应 case 中通过 `allowed_diffs` 标记预期差异。

## 运行测试

```bash
# 全部测试（单元 + 集成）
go test ./replaytest/... -v

# 仅集成测试
go test ./replaytest/... -run TestReplayConsistency_AllCases -v

# 仅单元测试
go test ./replaytest/... -run "Test(Normalize|Recursive|Wildcard|Apply|Build|Parse|Write|Has|Capture)" -v

# 竞态检测
go test ./replaytest/... -race -count=1

# 自定义报告路径
TRPC_AGENT_REPLAY_REPORT_PATH=./my_report.json go test ./replaytest/... -v
```
