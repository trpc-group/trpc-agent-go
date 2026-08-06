# 回放一致性测试

回放一致性测试用于验证同一组 session、memory、summary 和 track 操作在不同后端上的持久化结果是否一致。当前轻量矩阵只覆盖 `InMemory` 与 `SQLite`，不依赖外部服务，适合作为本地开发和 PR 检查中的快速回归。

可复用的 case、runner、snapshot、normalize、compare 和 report 编码位于 `session/replaytest`。`test/replay_consistency_test.go` 只负责 InMemory/SQLite wiring、具体 cases、故障注入和断言，因此新增后端可以复用同一执行与比较逻辑，而不必复制 e2e 测试实现。

`replaytest.Run` 要求传入非空且没有首尾空白的 run namespace。同一次逻辑比较中的所有后端必须复用同一个 namespace；重新运行同一个 case 时必须生成新的 namespace。namespace 与 case name 会共同写入 app、user 和 session 身份，从而在持久化服务上隔离 session state、memory、summary 和 track。创建 session 前，`Run` 会按 track、event、summary、直接 state map 作用域、顺序 memory、并发 memory 的固定顺序预检所有可静态判断的 fixture 错误。`Case.AppState` 和 `Case.UserState` 的 key 可以无前缀，也可以分别使用一层匹配的 `app:` / `user:` 前缀；已知跨作用域前缀仍作为 InMemory/SQLite 矩阵统一策略拒绝，未知前缀按普通 key 文本保留。`Case.SessionState` 只拒绝 `app:` 与 `user:`，无前缀、`temp:` 和自定义 key 保持允许。App/User 规范化只剥离最外层一层匹配前缀，因此 `app:app:flag` 等额外前缀仍保留。无效 track 配置、nil 或无法归一化的 event、非法 summary prefix、直接 state map 中不允许的跨作用域前缀或 canonical collision、已删除或未知 alias 的顺序 memory 操作，以及非 add 的并发 memory 操作都不会触发后端调用或留下 session。越过该边界后的后端相关失败仍可能保留部分回放数据；如需清理，应由调用方管理该生命周期。

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
  "session_id": "session-7-run_123-case_name",
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

当 map key 或 list index 只存在于一侧时，缺失侧的值保持为 `null`，并在
diff entry 层增加 `left_missing: true` 或 `right_missing: true`。missing
字段未出现表示该侧存在，即使它的真实值是 JSON `null`。例如，左侧缺失、
右侧合法用户值恰好为 `{"replay":"missing"}` 时，报告编码为：

```json
{
  "left": null,
  "right": {"replay": "missing"},
  "left_missing": true
}
```

库不会生成左右两侧同时 missing 的 diff。missing 标记不用于 nil snapshot
section：section 级 nil 仍是一个存在的 `null` 值，与空 map 或 list 比较时会
产生普通 diff，但不会设置 missing 标记。旧报告仍是合法 JSON，但其中原有
`{"replay":"missing"}` sentinel 的历史歧义无法事后恢复。

`context` 会按 section 携带定位信息，例如 `event_index`、`summary_filter_key`、`memory_key`、`left_memory_id`、`right_memory_id`、`track_name`、`track_event_index`。

## 比较范围

snapshot 覆盖以下 section：

- `session`：session ID、app、user ID
- `events`：消息、工具调用、工具响应、调用方提供的事件时间戳、branch、filter key、tag、state delta、extensions、actions
- `state`：session/app/user/temp state 合并后的可见状态，并以带标签的 byte value 表示，确保 nil、JSON、UTF-8 文本和二进制字节可区分
- `memory`：content、topics、metadata；raw memory ID 只用于 report 定位
- `summary`：`Session.Summaries[filterKey]`、summary text、topics、boundary metadata、`GetSessionSummaryText`
- `tracks`：track map key、外层 `TrackEvents.Track`、每个事件内嵌的 `TrackEvent.Track`、event order、payload、timestamp

normalize 时只移除重新生成的顶层 event ID 与后端生成的 memory ID。调用方提供的 `Event.Timestamp` 会保留为 UTC `RFC3339Nano` 字符串；非 nil 的 `Response.ID`、扁平化的 `Response.Created` 和嵌套在 `response.timestamp` 下的 `Response.Timestamp`（同样转为 UTC `RFC3339Nano`）都会保留。nil Response 与非 nil 但 metadata 为空或零值的 Response 保持可区分。JSON 归一化使用 `json.Decoder.UseNumber`，避免大整数精度丢失。业务字段差异不会默认放行。

当调用方手工构造的 snapshot 含 channel、function、NaN 或其他无法转换为 canonical JSON 比较表示的值时，`Compare` 与 `CompareSnapshots` 会返回错误而不是 panic。各 section 按 snapshot 顺序转换，每个 section 先 left 后 right，并在首个错误处停止；失败时不返回部分 diff，也不能通过 `allowed_diff` 放行。

Track payload fixture 支持完整 JSON 值域：object、array、string、number、boolean 和 null。持久化后的 `json.RawMessage` 使用带标签快照，kind 为 `nil`、`empty`、`json`、`utf8` 或 `base64`。合法 JSON 在 `payload.value` 内做 canonical normalize，同时保留 raw nil、空字节、JSON null、非 JSON UTF-8 文本和二进制字节之间的可观察差异。

每个 memory query 都声明 `ExpectedContents`。查询结果按照无序的精确内容多重集合比较，因此忽略后端特有的 ID、score 和排序，同时仍能发现缺失、无关、额外和重复结果。

Memory 操作别名按完整 canonical identity 解析，而不是只比较 content。Add alias 会比较 app、user、content、kind、event time、participants 和 location，并刻意排除 topics；相同 identity（包括幂等 Add）共享同一个 alias group，即使 Add 没有 `ResultAlias` 也会登记 active identity 供预检冲突检查。删除任一 alias 会使该 group 的全部 alias 失效，后续引用会在后端调用前返回 `memory alias "name" refers to deleted memory`；后续 Add 可以显式重新绑定已失效的同名 alias。Update 每次都会用后端返回的有效 ID 推进整个 group；如果目标 identity 已属于另一个 live group，则返回 collision 且不合并两个 group。因此内容或身份元数据导致 ID 轮换后，后续 update/delete 不会继续使用旧 ID。

Fixture event 预检与最终 snapshot 构建共用同一套 marshal-and-decode normalization。预检会在持久化前拒绝调用方提供的损坏 event；最终 snapshot 仍保留相同校验，用于发现执行后由后端或故障注入产生的污染。memory entry 为 nil、entry 的 `Memory` payload 为 nil、summary map 条目的值为 nil，或 track map 条目含 nil `TrackEvents` 容器时，snapshot 构建同样返回错误。这些情况不会在 normalize 时被丢弃，也不能通过 `allowed_diff` 放行；非 nil 的合法空 track 容器仍然有效。即使 session 为 nil，`BuildSnapshot` 也会校验并归一化传入的 memories，因此空 session 形式不会隐藏合法或损坏的 memory 数据。

包含直接 `Case.AppState`、`Case.UserState` 或 `Case.SessionState` 更新的 case，还会把这些 API 已明确区分的作用域作为后端契约直接校验。作用域由 case 字段和对应的 service 方法决定，不根据裸 key 猜测。`AppState` / `UserState` 接受无前缀或一层匹配前缀；已知跨作用域前缀仍由 harness 统一拒绝，未知前缀按普通 key 保留。原始 accepted map 会传给后端以覆盖真实 API 输入行为，prepared expected map 只剥离一个对应 App/User 前缀；`List*States` 返回值按已存储 key 形式复制，不会再次剥离。`flag` 与 `app:flag`、`locale` 与 `user:locale` 等 canonical collision 会在任何后端调用前报告，并包含 canonical key 和排序后的原始 key。`SessionState` 继续只拒绝 `app:` 与 `user:`，`temp:` 保留为 session-local key。没有直接 state 的 case 不会触发 ListApp/ListUser 或 peer 作用域校验。非法直接 state fixture 会在任何后端调用前返回；`InitialState` 与 `Event.StateDelta` 保持既有 key 语义。Runner 使用 `ListAppStates` / `ListUserStates` 比较直接 app/user 更新，随后在同一 app/user 下创建临时 peer session，并要求它只继承这些 app/user 值。每次 peer 创建尝试后都会使用脱离调用方取消信号的限时 context 尝试删除，包括已经落盘但返回错误的 ambiguous fail-after-write。app/user 传播缺失、session/temp state 泄漏和 peer 清理失败都是 runner error，不属于 snapshot diff，也不能通过 `allowed_diff` 放行。

`Event.StateDelta` 始终作为 session-local map 原样交给 `SessionService.AppendEvent`。Replay runner 不会把 `app:`、`user:`、`temp:` 或无前缀 key 自动路由到独立 store，也不会根据 key 前缀推断作用域。当前支持的 InMemory/SQLite 矩阵会把 event delta 保留在 session-local state；真实矩阵用例会覆盖多种 key 形态及覆盖顺序，检查独立 app/user store 保持不变，并比较最终 snapshot。MongoDB 不属于当前矩阵，也不属于本契约的支持范围。未来若要支持独立作用域路由，必须增加显式 capability/scope 元数据，并通过真实后端矩阵验证；本轮不实现。

## Summary 与 Track 策略

Go 版 summary 使用原生 session summary 语义，不生成 Python 风格的 summary event，也不比较 historical summary event。

每个 `SummaryStep` 可以通过 `EventPrefix` 指定执行 summary 前应已追加的 `Case.Events` 前缀长度。Runner 会在持久化前一次性解析完整 prefix 序列：前缀必须位于事件列表范围内并保持单调不减，允许相同前缀，nil 表示全部事件；时间线执行阶段只消费这些已验证 target。因此用例可以表达“追加事件、总结、继续追加、再次总结”，同时避免较晚的 fixture 错误在较早 event/summary 已落盘后才被发现。

提供 `Backend.CreateSummary` 时，该回调负责单个 step 的完整操作，包括 fixture 特定的 summary 准备和持久化，并且必须支持共享 backend 的并发 `Run`。回调为 nil 时，runner 直接调用 `SessionService.CreateSessionSummary`。

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

测试框架包含五类异常注入：

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
- quoted key 只有在解码后的值包含非 `*` 字面内容时才提供具体性；`"*"`、`"**"` 和 `"\u002a"` 等转义后的纯星号仍属于纯通配，不能使规则有效
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
- 提供 `Backend.ReadAllMemories`；alias 解析与最终 snapshot 共用该 callback，并且只有在后端专用分页、total count 校验或明确的无上限读取已经证明所有 memory 均被返回时，才能设置 `complete=true`
- 后端生成的 ID 与 response 时间元数据通过 normalize 处理，同时保留调用方提供的事件时间戳
- summary 与 track 语义与现有后端一致
- 新后端差异必须先由异常注入测试证明可定位，再评估是否需要 `allowed_diff`
