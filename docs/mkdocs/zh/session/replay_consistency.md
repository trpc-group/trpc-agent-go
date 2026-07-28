# 回放一致性测试

回放一致性测试用于验证同一组 session、memory、summary 和 track 操作在不同后端上的持久化结果是否一致。当前轻量矩阵只覆盖 `InMemory` 与 `SQLite`，不依赖外部服务，适合作为本地开发和 PR 检查中的快速回归。

可复用的 case、runner、snapshot、normalize、compare 和 report 编码位于 `session/replaytest`。`test/replay_consistency_test.go` 只负责 InMemory/SQLite wiring、具体 cases、故障注入和断言，因此新增后端可以复用同一执行与比较逻辑，而不必复制 e2e 测试实现。

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
- `events`：消息、工具调用、工具响应、调用方提供的事件时间戳、branch、filter key、tag、state delta、extensions、actions
- `state`：session/app/user/temp state 合并后的可见状态，并以带标签的 byte value 表示，确保 nil、JSON、UTF-8 文本和二进制字节可区分
- `memory`：content、topics、metadata；raw memory ID 只用于 report 定位
- `summary`：`Session.Summaries[filterKey]`、summary text、topics、boundary metadata、`GetSessionSummaryText`
- `tracks`：track map key、外层 `TrackEvents.Track`、每个事件内嵌的 `TrackEvent.Track`、event order、payload、timestamp

后端重新生成的 event ID、response ID 与时间元数据，以及后端生成的 memory ID 会在 normalize 时移除。调用方提供的 `Event.Timestamp` 会保留为 UTC `RFC3339Nano` 字符串，使持久化时间漂移能够被发现。JSON 归一化使用 `json.Decoder.UseNumber`，避免大整数精度丢失。业务字段差异不会默认放行。

Track payload fixture 支持完整 JSON 值域：object、array、string、number、boolean 和 null。持久化后的 `json.RawMessage` 使用带标签快照，kind 为 `nil`、`empty`、`json`、`utf8` 或 `base64`。合法 JSON 在 `payload.value` 内做 canonical normalize，同时保留 raw nil、空字节、JSON null、非 JSON UTF-8 文本和二进制字节之间的可观察差异。

每个 memory query 都声明 `ExpectedContents`。查询结果按照无序的精确内容多重集合比较，因此忽略后端特有的 ID、score 和排序，同时仍能发现缺失、无关、额外和重复结果。

Memory 操作别名按完整 canonical identity 解析，而不是只比较 content。Add alias 会比较 app、user、content、kind、event time、participants 和 location，并刻意排除 topics。Update 每次都会用后端返回的有效 ID 推进原 `Ref` alias，因此内容或身份元数据导致 ID 轮换后，后续 update/delete 不会继续使用旧 ID。

包含 app、user 或 session state 的 case 还会直接验证作用域契约。Runner 分别读取 app/user state，并在同一 app/user 下创建临时 peer session；peer 必须只继承 app/user 值，且会在所有返回路径中删除。app/user 传播缺失、session/temp state 泄漏和 peer 清理失败都是 runner error，不属于 snapshot diff，也不能通过 `allowed_diff` 放行。

## Summary 与 Track 策略

Go 版 summary 使用原生 session summary 语义，不生成 Python 风格的 summary event，也不比较 historical summary event。

每个 `SummaryStep` 可以通过 `EventPrefix` 指定执行 summary 前应已追加的 `Case.Events` 前缀长度。前缀必须位于事件列表范围内并保持单调不减，允许相同前缀；nil 保持默认的“全部事件后执行”。因此用例可以表达“追加事件、总结、继续追加、再次总结”，并验证持久化 boundary 确实推进。

summary 比较重点：

- full summary：`session.SummaryFilterKeyAllContents`
- filter-key summary，例如 `root/tools/weather`
- summary overwrite/update
- `SummaryBoundary` 的 version、filter key、cutoff，以及归一化后的 last-event 锚点
- `GetSessionSummaryText` 返回值

非空但无法映射到当前 snapshot 事件列表的 summary boundary anchor 会表示为 `last_event_index: -1`。

track 比较重点：

- track map-key name
- 外层 `TrackEvents.Track` 容器身份
- 每个 `TrackEvent.Track` 值
- 同一 track 下事件顺序
- 带标签的 payload 表示；合法 JSON 位于 `payload.value`
- 固定 timestamp

注意：`AppendTrackEvent` 会维护 `state["tracks"]`。如果调试 track diff，同时也要留意 state section 中的 track index。

## 异常检测

测试框架包含三类异常注入：

- snapshot mutation：partial event loss、event timestamp drift、summary loss、wrong session attribution、wrong summary filter key、large JSON-number drift、state byte representation drift、track payload drift、embedded track drift、outer track-container drift、track order drift
- service-contract mutation：事件交错追加后仍返回旧 summary boundary、错误的外层 track 身份，以及 JSON null 被恢复成 nil raw payload
- 执行中 retry：fail-before-write 使用相同输入重试后必须与单次成功 baseline 一致；ambiguous fail-after-write 验证 Memory Add、state update 和 summary overwrite 的幂等结果
- SQLite/public API injection：state pollution、memory pollution、summary overwrite
- SQLite/storage injection：直接注入 duplicate memory row，用于模拟存储损坏，并验证它会被报告为 unallowed memory diff

这些异常默认都必须产生 unallowed diff。正常 replay matrix 的误报必须为 0。

当前 `AppendEvent` 没有按 event ID 去重。测试会真实模拟“首次已经写入但返回错误，随后重试相同 event”，并要求重复 event 在 index 1 的错位内容和 index 2 的额外尾部位置被报告为 unallowed diff。该测试验证 harness 的诊断能力，不改变 Session 后端的运行时幂等语义。

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
- `path` 必填，必须从声明的 section 根路径开始，并且在该根路径之后包含具体字段、quoted key 或固定索引
- `$`、`$*`、`$.*`、`$[*]` 等全局根模式，`*`、`**`、`***` 等纯通配符，以及 `$.memory`、`$.memory*`、`$.memory.*`、`$.memory[*]` 等 section 根模式均无效
- `backend_a` 和 `backend_b` 必填，不能是空字符串或 `*`
- `reason` 必填且不能为空白
- backend pair 支持左右顺序互换
- `path` 支持局部 glob，例如 `$.memory[*].content`

ID 和时间类差异应优先通过 normalize 或 runner 修正，不应使用 `allowed_diff` 放行。

## 扩展后端

当前 runnable matrix 只包含 `InMemory` 与 `SQLite`。Redis、PostgreSQL、MySQL、ClickHouse 等外部后端暂不进入轻量矩阵，后续应通过 env-gated backend factory 接入，避免默认测试依赖外部服务。

接入新后端时应保持：

- 默认本地测试不需要外部服务
- backend 名称非空、没有首尾空白；该名称同时是 report 与 `allowed_diff` 使用的身份
- 配置 memory service 支持的正数 `Backend.MemoryReadLimit`；alias 解析与最终 snapshot 共用该上限，返回条目数触及上限时 runner 会失败，而不会比较可能被截断的结果
- 后端生成的 ID 与 response 时间元数据通过 normalize 处理，同时保留调用方提供的事件时间戳
- summary 与 track 语义与现有后端一致
- 新后端差异必须先由异常注入测试证明可定位，再评估是否需要 `allowed_diff`
