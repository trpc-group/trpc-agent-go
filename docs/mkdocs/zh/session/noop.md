# 无持久化（Noop）

当上游业务已经维护完整对话历史，或者不希望在请求之间保留 Session 历史和状态时，可以使用 `session/noop`。Noop 不会跨请求保存 Session、事件或状态，因此 Session 存储不会随着 Session ID 数量增加而持续增长。

Noop 并不是把 Runner 内部的 Session 对象设置为 `nil`。Runner 在单次 `Run` 内仍会创建临时 Session，使 Graph、Chain、Tool 等组件能够访问本轮消息和状态增量；本轮结束后，这些数据不会保存在 Session Service 中。

## 配合 `RunWithMessages` 使用

业务自行维护历史时，应在每次请求中传入完整历史：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessionnoop "trpc.group/trpc-go/trpc-agent-go/session/noop"
)

sessionService := sessionnoop.NewService()
r := runner.NewRunner(
    "my-agent",
    agent,
    runner.WithSessionService(sessionService),
)

// history 由业务服务持久化和更新。
history := []model.Message{
    model.NewSystemMessage("你是一个有帮助的助手"),
    model.NewUserMessage("我叫张三"),
    model.NewAssistantMessage("你好，张三"),
    model.NewUserMessage("我叫什么名字？"),
}

eventChan, err := runner.RunWithMessages(
    ctx,
    r,
    "user123",
    "session-001",
    history,
)
```

与持久化 Session 不同，Noop 的下一次调用不会自动恢复本次历史。因此，业务需要消费 `eventChan`、保存 Agent 回复，并在下一次调用 `RunWithMessages` 时再次传入更新后的完整历史。

## 行为边界

| 能力 | Noop 行为 |
| --- | --- |
| 单次 `Run` 内的消息和状态增量 | 支持 |
| 跨请求对话历史和 Session 状态 | 不保存 |
| `GetSession` / `ListSessions` | 不返回持久化数据 |
| Session 摘要和摘要恢复 | 不支持 |
| 基于 Session 的 AG-UI 历史快照、await-user-reply 路由和 Session 恢复 | 不支持 |

Noop 只控制 Session Service。它不会禁用独立配置的 Memory、Artifact、Session Ingestor、Evolution 或 Graph checkpoint 服务；这些服务仍可能根据各自配置跨请求保存或恢复数据。

即使使用 Noop，Runner API 中的 `userID` 和 `sessionID` 仍需提供，用于构造并校验本轮临时 Session 的标识。

## 适用场景

- 上游业务服务已经负责保存完整对话历史
- 每次请求都显式携带完整 Session 上下文的 API
- 不需要跨请求 Session 状态、摘要、历史快照或恢复能力
- 希望保留 Graph、Chain、Tool 在单次运行内对临时 Session 的依赖

如果只需要 Runner 在进程存活期间自动恢复上一轮历史，可以使用[内存存储](inmemory.md)。如果需要跨进程重启保留 Session 数据或在多个实例间共享，请使用持久化后端。

## 相关示例

- [Noop + RunWithMessages 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runwithmessages)
