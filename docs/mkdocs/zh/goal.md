# Goal 扩展

Goal 扩展给 `LLMAgent` 增加一个会话级目标契约。它适合这类场景：用户希望
Agent 围绕一个更大的目标持续推进，直到目标完成，或明确进入阻塞状态。

这是 Agent 扩展，不是 Runner 模式：

- 通过 `llmagent.WithExtensions(goal.New())` 安装；
- 模型会看到 `get_goal`、`create_goal`、`update_goal` 三个工具；
- goal 状态存储在 session state；
- 流式过程输出可以正常发出，但过早的 final response 会在同一次模型循环内被拦住；
- 外层 `Runner.Run` 仍然只产生一个 `runner.completion`。

## 使用方式

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/extension/goal"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

ag := llmagent.New(
    "planner",
    llmagent.WithModel(modelInstance),
    llmagent.WithExtensions(goal.New(
        goal.WithMaxRetries(3),
    )),
)
```

如果业务希望支持 `/goal <objective>`，应该在业务命令层解析，然后在调用
`Runner.Run` 前创建 session goal：

```go
key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
_, err := goal.Start(ctx, sessionService, key, objective)
```

框架核心不解析 slash command。

## 语义

当 goal 处于 active 状态时，模型给出 final response 并不代表任务完成。模型必须
继续推进，或者调用 `update_goal`：

- `complete`：目标已经真实完成；
- `blocked`：同一个阻塞条件已经在多次 goal 尝试中重复出现，并且继续推进必须依赖
  用户输入或外部状态变化。

如果模型反复过早结束，扩展会按 `WithMaxRetries` 的配置重试。重试耗尽后，
response 会原样放行，避免一次运行陷入无限循环。

Goal 不改变 streaming 配置。是否流式输出仍由 `LLMAgent` 的生成配置或
`agent.WithStream(...)` 等运行参数决定。调用方可能先看到一段阶段性输出，随后 Agent
继续推进；这段输出不等于 goal 已经完成。真正的运行结束仍以 `runner.completion`
为准。

## 边界

- 扩展安装在一个 `LLMAgent` 上，子 Agent 不会自动继承。
- 多个 Agent 可以通过相同 state key 共享同一个 session goal，但通常建议只安装在
  拥有“是否完成”判断权的 Agent 上。
- `token_budget` 不属于这个扩展，预算控制应作为独立运行时策略设计。
- 并发控制仍由调用方负责。同一个 session 并发运行时，状态写入语义取决于所选
  `session.Service`。

可运行示例见 `examples/goal`。
