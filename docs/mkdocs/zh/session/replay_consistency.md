# 回放一致性测试

回放一致性测试用于验证同一组 session、memory、summary 和 track 操作在不同后端上的持久化结果是否一致。当前轻量矩阵只覆盖 `InMemory` 与 `SQLite`，不依赖外部服务，适合作为本地开发和 PR 检查中的快速回归。

## 运行方式

在仓库根目录下运行 targeted 测试：

```bash
cd test
CGO_ENABLED=1 go test ./... -run ReplayConsistency -count=1
```

也可以运行整个 e2e module：

```bash
cd test
CGO_ENABLED=1 go test ./... -count=1
```

SQLite 后端使用 `github.com/mattn/go-sqlite3`，因此需要启用 CGO 并提供 C 编译器。

## 报告

默认报告路径为仓库根目录：

```text
session_memory_summary_track_diff_report.json
```

可以通过环境变量覆盖：

```bash
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REPORT_PATH=replay-report.json go test ./... -run ReplayConsistency -count=1
```

正常矩阵期望报告内容为：

```json
[]
```

每条 diff report entry 都包含：

```json
{
  "case": "case_name",
  "session_id": "session-case_name",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "section": "summary",
  "path": "$.summary[\"root/tools/weather\"].summary",
  "left": "left value",
  "right": "right value",
  "allowed": false,
  "reason": "",
  "context": {
    "summary_filter_key": "root/tools/weather"
  }
}
```

`context` 会按 section 携带定位信息，例如 `event_index`、`summary_filter_key`、`memory_key`、`left_memory_id`、`right_memory_id`、`track_name`、`track_event_index`。

## 比较范围

snapshot 覆盖以下 section：

- `session`：session ID、app、user ID
- `events`：消息、工具调用、工具响应、branch、filter key、tag、state delta、extensions、actions
- `state`：session/app/user/temp state 合并后的可见状态，并以带标签的 byte value 表示，确保 nil、JSON、UTF-8 文本和二进制字节可区分
- `memory`：content、topics、metadata；raw memory ID 只用于 report 定位
- `summary`：`Session.Summaries[filterKey]`、summary text、topics、boundary metadata、`GetSessionSummaryText`
- `tracks`：track name、每个事件内嵌的 track、event order、payload、timestamp

生成型字段通过 normalize 处理，例如 event ID、response ID、timestamp 和后端生成的 memory ID。JSON 归一化使用 `json.Decoder.UseNumber`，避免大整数精度丢失。业务字段差异不会默认放行。

## Summary 与 Track 策略

Go 版 summary 使用原生 session summary 语义，不生成 Python 风格的 summary event，也不比较 historical summary event。

summary 比较重点：

- full summary：`session.SummaryFilterKeyAllContents`
- filter-key summary，例如 `root/tools/weather`
- summary overwrite/update
- `SummaryBoundary` 的 version、filter key、cutoff，以及归一化后的 last-event 锚点
- `GetSessionSummaryText` 返回值

非空但无法映射到当前 snapshot 事件列表的 summary boundary anchor 会表示为 `last_event_index: -1`。

track 比较重点：

- track name
- 每个 `TrackEvent.Track` 值
- 同一 track 下事件顺序
- payload canonical JSON
- 固定 timestamp

注意：`AppendTrackEvent` 会维护 `state["tracks"]`。如果调试 track diff，同时也要留意 state section 中的 track index。

## 异常检测

测试框架包含三类异常注入：

- snapshot mutation：partial event loss、summary loss、wrong session attribution、wrong summary filter key、large JSON-number drift、state byte representation drift、track payload drift、embedded track drift、track order drift
- SQLite/public API injection：duplicate event、state pollution、memory pollution、summary overwrite
- SQLite/storage injection：直接注入 duplicate memory row，用于模拟 backend retry bug 或 duplicate retry effect，并验证它会被报告为 unallowed memory diff

这些异常默认都必须产生 unallowed diff。正常 replay matrix 的误报必须为 0。

## allowed_diff

`allowed_diff` 只用于显式记录已知且可接受的差异。默认不允许任何业务字段差异。

示例：

```json
{
  "section": "memory",
  "path": "$.memory[*].content",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "reason": "known backend-specific normalization gap"
}
```

规则：

- `section` 必填，不能是空字符串或 `*`
- `path` 必填，不能是空字符串，也不能是 `*`、`**`、`***` 这类纯通配符
- `backend_a` 和 `backend_b` 必填，不能是空字符串或 `*`
- `reason` 必填且不能为空白
- backend pair 支持左右顺序互换
- `path` 支持局部 glob，例如 `$.memory[*].content`

ID 和时间类差异应优先通过 normalize 或 runner 修正，不应使用 `allowed_diff` 放行。

## 扩展后端

当前 runnable matrix 只包含 `InMemory` 与 `SQLite`。Redis、PostgreSQL、MySQL、ClickHouse 等外部后端暂不进入轻量矩阵，后续应通过 env-gated backend factory 接入，避免默认测试依赖外部服务。

接入新后端时应保持：

- 默认本地测试不需要外部服务
- 生成型 ID/时间字段通过 normalize 处理
- summary 与 track 语义与现有后端一致
- 新后端差异必须先由异常注入测试证明可定位，再评估是否需要 `allowed_diff`
