# 回放一致性

应用通常先基于内存版 Session 与 Memory 后端开发，之后再切换到 SQLite、Redis
或 SQL 数据库。一旦不同后端在事件顺序、state、memory 或 summary 上产生分歧，
故障往往在离根因很远的地方才暴露出来：回放错乱、上下文丢失、长期记忆被污染、
摘要相互覆盖。

回放一致性框架把这类问题变成可执行的测试。它用同一份确定性操作脚本驱动多个
后端，把每个后端实际持久化的内容投影为统一形式，然后报告所有存在差异的字段。

框架位于 `test` 模块的
[`test/replayconsistency`](https://github.com/trpc-group/trpc-agent-go/tree/main/test/replayconsistency)。

## 运行方式

轻量模式不依赖任何外部服务，CI 跑的就是它：

```bash
cd test
go test ./replayconsistency/
```

参与对比的有三个后端：内存、基于临时文件的 SQLite，以及跑在进程内服务上的
Redis。内存后端作为基准，因为它是应用最先使用的实现。

重新生成示例差异报告：

```bash
go test ./replayconsistency/ -run TestReport -update-report
```

产物写入
`test/replayconsistency/testdata/session_memory_summary_track_diff_report.json`。

### 集成模式

集成后端通过环境变量开启，未设置时自动跳过：

| 变量 | 后端 | 示例 |
| --- | --- | --- |
| `TRPC_REPLAY_REDIS_URL` | 真实 Redis 服务 | `redis://127.0.0.1:6379` |

```bash
TRPC_REPLAY_REDIS_URL=redis://127.0.0.1:6379 go test ./replayconsistency/
```

每次运行使用独立的 key 前缀，因此即使指向共享服务，也不会读到其他运行的数据。

新增后端需要在 `IntegrationBackends` 里加一项，并在 `test/go.mod` 中加上
`require`。本仓库内的后端都是独立模块，因此还需要一条指向本地检出的 `replace`：

```bash
cd test
go mod edit -replace=trpc.group/trpc-go/trpc-agent-go/session/postgres=../session/postgres
go mod tidy
```

缺少 `replace` 时，框架会解析到已发布版本，于是拿当前检出去和某个 release 比对，
而不是和自身比对。无论哪种情况，用例和比较器都无需改动，因为它们只依赖
`session.Service` 与 `memory.Service`。

## 用例

| 用例 | 检验目标 |
| --- | --- |
| `single-turn` | 单轮：一条 user 消息与一条 assistant 回复 |
| `multi-turn-ordering` | 三轮对话的读取顺序 |
| `tool-call-round-trip` | 工具调用、工具响应、参数与 extensions |
| `state-write-overwrite-clear` | session/app/user 三种作用域；nil 与删除的区别 |
| `memory-write-update-delete` | 改内容与只改 topics 时的 ID 轮换行为 |
| `summary-generate-and-update` | 全会话与分支摘要，以及重新生成 |
| `summary-with-event-truncation` | 摘要覆盖早期轮次，其后保留原始事件 |
| `track-events` | 两条 track 上的耗时、状态与错误记录 |
| `interleaved-out-of-order-writes` | 两个 invocation 以乱序时间戳交错追加 |
| `retry-and-recovery` | 重试后重复写入的事件、memory 与摘要 |

每个用例都以一条 user 消息开头，这是硬性要求而非风格偏好。
`session.ApplyEventFiltering` 在每次追加时都会执行，并把事件列表锚定在 user
消息上：第一条 user 消息之前的事件会被截断，而完全不含 user 消息的会话读回
来是空的。因此只由 assistant 事件构成的用例在所有后端上都观察不到任何内容，
而「一致地为空」与「一致」是无法区分的。

## 设计说明

**归一化。** 用例自带事件 ID，并以「相对本次运行基准时刻的偏移」表达时间，
从而在比较开始前就消除了两个最大的不确定性来源。所有后端共享同一个基准时刻，
因此拿到的绝对时间戳完全相同。时间戳按毫秒截断后以偏移量比较；JSON 载荷与工具
参数按键排序后重新编码；map 投影为按键排序的切片；topics 这类无序集合做排序。
基准时刻比墙钟提前一分钟，因为后端可能丢弃早于其所属 session 的数据。

**比较。** 比较器通过反射遍历投影结构，而不是手写逐字段比较，因此新增字段会
自动纳入比较，而不会被悄悄漏掉。带 key 的元素（如按 filter key 索引的 summary、
按 ID 索引的 memory）按 key 匹配，缺失元素会被报告为缺失，而不会导致其后所有
元素错位；事件与 track 条目按位置匹配，因为顺序本身就是契约的一部分。差异记录
携带形如
`sessions[ref="app/u1/s1"].summaries[filterKey="tool"].text` 的路径。

**分类。** 差异分为 allowed、known、fatal 三类。allowed 表示两个后端都有权如此，
这份清单目前是空的，且这正是预期状态——迄今观察到的差异没有一条配得上被认定为
正常行为；known 表示差异真实存在，在结论未定之前先带证据记录而不是让构建失败；
其余一律失败。像 `ReadMemories` 在写入时间戳相同时的返回顺序这类「构造上就不确定」
的值，会用带说明的标签从投影中排除，而不是先比较再豁免，这样报告才是可复现的。
summary 还会投影它实际挂在哪个 session 下，因此「摘要归属错误」是可见的，而不只是
表现为缺失。

**自检。** 故障注入会刻意破坏某一个后端，并要求比较器必须发现：事件丢失、重复、
乱序，摘要丢失、filter key 错误、归属 session 错误，state key 丢失与泄漏，以及
track 条目丢失。没有这一层，整套用例只能说明「当前各后端一致」，而一个什么都不
检查的比较器也会给出同样的结论。

## 已知差异

轻量模式当前记录了以下差异。它们会出现在报告中，但不会让构建失败。

- **乱序追加的回放顺序不同。** 以乱序时间戳追加的事件，在 Redis 上按时间戳顺序
  读回，在内存与 SQLite 上按追加顺序读回。Redis 侧用
  `ZADD <key> <timestamp> <eventID>` 建立索引并按 score 读取。这是 Redis 有序
  集合的正常语义，与进程内服务无关。
- **重试的事件在 Redis 上会被合并。** 重复追加同一 ID 的事件会覆盖原记录，因为
  事件以 `HSET <key> <eventID> <json>` 存储；SQLite 对事件 ID 没有唯一约束，
  内存实现则无条件追加。`session.Service` 未声明 `AppendEvent` 是否幂等，因此
  两种行为都没有违反明文契约。
- **nil 状态增量在 Redis 上不生效。** 该现象基于进程内服务观察：其 Lua 把 JSON
  null 解码为 Lua nil，而 Lua table 无法保存 nil 值，导致增量脚本中的判空条件
  看到空表并跳过更新。真实 Redis 会把 null 解码为 `cjson.null` 哨兵值，行为可能
  不同，因此在通过集成模式确认之前，此项只做记录而不作为断言。
