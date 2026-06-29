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
    // 可选：为该 LLMAgent 的一次 invocation 设置硬上限。
    llmagent.WithMaxLLMCalls(20),
    llmagent.WithMaxToolIterations(10),
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

`WithMaxRetries` 只限制“过早 final response -> 提醒 -> 继续”的续跑次数。业务还可以在
同一个 `LLMAgent` 上配置 `WithMaxLLMCalls` 和 `WithMaxToolIterations`，为该 Agent 的
一次 invocation 中的模型调用和工具调用轮次设置硬上限。直接将 Goal 安装在 Runner 的主
`LLMAgent` 上时，这通常等同于限制一次 `Runner.Run`。达到上限会停止本次运行，但不会自动
把 session goal 改为 `complete` 或 `blocked`；后续新的 invocation 会使用新的调用额度。
因此它们适合作为运行级熔断，而不是 goal 的终态判断。

Goal 不改变 streaming 配置。是否流式输出仍由 `LLMAgent` 的生成配置或
`agent.WithStream(...)` 等运行参数决定。调用方可能先看到一段阶段性输出，随后 Agent
继续推进；这段输出不等于 goal 已经完成。真正的运行结束仍以 `runner.completion`
为准。

Goal 工具需要串行语义。不要在安装 Goal 扩展的同一个 `LLMAgent` 上启用
`llmagent.WithEnableParallelTools(true)`。模型不应在同一个 parallel tool batch 中同时调用
`create_goal` 和 `update_goal`，因为并行工具执行会为每个工具调用使用隔离的
invocation/session 视图。如果业务工具需要并行执行，建议让 Goal 继续由串行的 owner agent
管理，或把并行业务工作放到另一个 agent 中。

## 边界

- 扩展安装在一个 `LLMAgent` 上，子 Agent 不会自动继承。
- 多个 Agent 可以通过相同 state key 共享同一个 session goal，但通常建议只安装在
  拥有“是否完成”判断权的 Agent 上。
- `token_budget` 不属于这个扩展，预算控制应作为独立运行时策略设计。
- 并发控制仍由调用方负责。同一个 session 并发运行时，状态写入语义取决于所选
  `session.Service`。

可运行示例见 `examples/goal`。
