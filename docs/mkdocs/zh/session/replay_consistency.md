# Session Replay 一致性

`session/replaytest` 会把同一组强类型操作写入多个 Session / Memory
后端，读取中间 checkpoint 与最终状态，消除后端噪声后进行字段级比较，并输出
结构化差异。公开矩阵包含 12 个 case，覆盖普通/多轮对话、工具调用、State、
Memory、Summary 覆盖与截断恢复、Track、并发、重试恢复和重复身份保持。

## 轻量模式

默认集成测试比较：

- InMemory Session + InMemory Memory
- SQLite Session + SQLite Memory

无需 API Key 或外部服务。SQLite 依赖 CGO 和 C 编译器。

```bash
cd test
CGO_ENABLED=1 go test . -run ReplayConsistency -count=1
```

测试还会验证每个公开 case 至少能检出一种人为异常，并额外覆盖公共 Service
故障包装器和 SQLite 存储层破坏。轻量完整运行设置了 30 秒上限；正常 replay
应当没有 blocking diff。

## 可选集成模式

运行同一命令前，可设置：

| 后端 | 环境变量 |
| --- | --- |
| Redis | `TRPC_AGENT_REPLAY_REDIS_URL` |
| PostgreSQL | `TRPC_AGENT_REPLAY_POSTGRES_DSN` |
| MySQL | `TRPC_AGENT_REPLAY_MYSQL_DSN` |
| ClickHouse | `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` |

未设置的集成会明确 skip；已启用后端使用唯一 table/key prefix。可选 Session
后端目前搭配 InMemory Memory，以隔离 Session 能力差异。ClickHouse Session
未实现 `session.TrackService`，因此 Track 被声明为
unsupported/allowed_diff。

## 设计说明（约 260 字）

框架将操作执行、采集、归一化、比较和报告分层。逻辑身份账本把后端生成的
event、invocation、tool-call、memory ID 映射到 case 定义的稳定身份，避免按
顺序猜测 ID，也不会折叠内容相同的重复记录。Event 会移除时间戳、request
ID、私有 timing 字段和显式声明的耗时项；State 字节则保留 nil、JSON、UTF-8
或 base64 标签，并用 `UseNumber` 防止大整数失真。Memory 保留数量与 metadata，
支持精确/无序检索策略及有限精度 score。

Summary 同时比较所属 app/user/session、map key 与内嵌 filter-key、正文、版本、
覆盖 checkpoint、更新时间/截断 event index 和最后事件逻辑 ID。Track 保留
track name 与时序，递归归一化 invocation/tool 引用，仅删除明确易变的耗时
字段。并发 case 先验证事件数量及 happens-before，再规范化合法的分支交错。
`allowed_diff` 必须精确指定两个后端、section、完整字段路径和解释，禁止通配
掩盖真实错误。后端能力需显式声明，unsupported 会进入报告。轻量模式使用
InMemory/SQLite；Redis、PostgreSQL、MySQL、ClickHouse 通过环境变量开启。
报告采用单写入器、临时文件、fsync 与 rename 发布。

## 差异报告

示例文件：

```text
test/testdata/session_memory_summary_track_diff_report.json
```

每条 diff 包含 case、session ID、后端、字段路径、两侧值/存在性、
checkpoint、`allowed_diff` 和解释；适用时还包含 event index、memory ID、
summary ID/filter-key 或 track name。
