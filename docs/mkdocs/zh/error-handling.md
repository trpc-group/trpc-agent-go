# Error Handling

## 为什么需要这套设计

Agent 应用里，错误处理通常同时需要两类信息：

- 给程序分支、重试、监控系统用的结构化错误信号
- 给业务排障、回放、上报系统用的稳定业务错误信息

在 graph 工作流里，这些信息可能来自：

- 本地节点
- 子图 / sub-agent
- 远端 A2A agent

tRPC-Agent-Go 现在为这三条链路提供了一条统一路径。

## 设计目标

这套方案遵循四条规则：

1. `Response.Error` 继续作为事件流里的失败信号。
2. 业务可见的错误集合沉淀在 graph state 中。
3. 可恢复错误继续执行，但不能丢记录。
4. 致命错误即使提前终止，也要先把 fallback 业务状态发出去。

## 核心能力

### `graph.ExecutionError`

`graph.ExecutionError` 是框架统一的业务错误记录结构，存放在 state 里。

它包含：

- `Severity`：`recoverable` 或 `fatal`
- `NodeID` / `NodeName` / `NodeType`
- `StepNumber`
- `Timestamp`
- `Error`：结构化的 `*model.ResponseError`

### `graph.ExecutionErrorCollector`

`graph.ExecutionErrorCollector` 是推荐使用的官方 helper。

它提供：

- 可直接挂到 schema 的 state field 和 reducer
- 节点错误收集回调
- 子图错误回传到父图的 output mapper

### `graph.EmitCustomStateDelta`

致命错误有一个典型问题：graph 可能在真正发出最终 `graph.execution`
快照之前就结束了。

`graph.EmitCustomStateDelta(...)` 用来解决这个问题。它会在错误路径上立刻发
一条带 `StateDelta` 的自定义事件，把业务 fallback 状态先送出去。

`ExecutionErrorCollector` 在 fatal 场景下会自动使用它。

## 推荐的 graph 用法

### 1. 给 schema 增加错误字段

```go
schema := graph.MessagesStateSchema()

collector := graph.NewExecutionErrorCollector()
collector.AddField(schema)
```

默认 key 是 `graph.StateKeyExecutionErrors`。

如果你要自定义 key：

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorStateKey("node_errors"),
)
collector.AddField(schema)
```

### 2. 注册 collector 回调

```go
sg := graph.NewStateGraph(schema).
    WithNodeCallbacks(collector.NodeCallbacks())
```

这是最简单、最推荐的框架级接入方式。凡是进入 `AfterNode` 的节点错误，都会
被记录到 collector 对应的 state field 中。

### 3. 决定哪些错误可以恢复

默认情况下，collector 只负责记录，不会帮你继续执行。

如果某些错误是可恢复的，可以配置 policy：

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorPolicy(func(
        ctx context.Context,
        cb *graph.NodeCallbackContext,
        state graph.State,
        err error,
    ) graph.ExecutionErrorPolicy {
        if errors.Is(err, errQuotaSoftLimit) {
            return graph.ExecutionErrorPolicy{
                Recover: true,
            }
        }
        return graph.ExecutionErrorPolicy{}
    }),
)
```

当 `Recover` 为 `true` 时，collector 会：

- 写入一条 `recoverable` 记录
- 返回替代节点结果，让 graph 继续执行

### 4. 恢复时可选自定义 replacement

如果可恢复错误需要带着自定义状态更新或跳转继续，可以使用
`ExecutionErrorPolicy.Replacement`。

推荐 replacement 类型：

- `graph.State`
- `*graph.Command`

collector 会把 `execution_errors` 的更新自动合并进去。

```go
collector := graph.NewExecutionErrorCollector(
    graph.WithExecutionErrorPolicy(func(
        ctx context.Context,
        cb *graph.NodeCallbackContext,
        state graph.State,
        err error,
    ) graph.ExecutionErrorPolicy {
        if !errors.Is(err, errRemoteCacheMiss) {
            return graph.ExecutionErrorPolicy{}
        }
        return graph.ExecutionErrorPolicy{
            Recover: true,
            Replacement: &graph.Command{
                Update: graph.State{
                    "cache_status": "miss",
                },
                GoTo: "fallback_builder",
            },
        }
    }),
)
```

## 运行结束后怎么拿错误

### 只消费 graph 事件

如果这次运行正常到达结束点，就从最终 `graph.execution` 事件的 `StateDelta`
里读取 collector key。

```go
errors, err := graph.ExecutionErrorsFromStateDelta(
    evt.StateDelta,
    graph.StateKeyExecutionErrors,
)
```

### 消费 Runner 事件

如果 fatal 错误导致 graph 在发出 `graph.execution` 之前就结束，Runner 现在会
把 fallback 业务状态复制到最后那条 `runner.completion` 上。

因此应用层可以用一个统一规则：

- 一直消费到 `runner.completion`
- 从它的 `StateDelta` 里读取 collector key
- 真正的 transport failure 仍然看更早那条 fatal 事件的 `Response.Error`

## Subgraph / sub-agent

这里通常有两类诉求。

### 子执行过程里的实时观测

如果你要做流式 SSE 观测、日志或指标，可以使用：

- `graph.WithAgentNodeEventCallback(...)`
- 或 graph 级 `RegisterAgentEvent(...)`

这类回调适合做实时观测，不建议把它作为最终状态持久化入口。

### 子结果回传到父图

最终推荐用 collector 自带的 subgraph mapper：

```go
collector := graph.NewExecutionErrorCollector()

sg.AddAgentNode(
    "child_agent",
    "planner",
    graph.WithSubgraphOutputMapper(
        collector.SubgraphOutputMapper(),
    ),
)
```

它同时支持两种情况：

- 子图正常完成，产生 `graph.execution`
- 子图 fatal 终止，但在终止前先发出了 fallback state

对自定义 mapper 来说，这两类结果现在会明确分开：

- `SubgraphResult.FinalState` 和 `SubgraphResult.RawStateDelta` 只表示正常
  结束时的 `graph.execution` 快照
- `SubgraphResult.FallbackState` 和 `SubgraphResult.FallbackStateDelta`
  只表示 fatal child 的 fallback state

如果你就是想用一套逻辑同时处理两种情况，可以使用：

- `SubgraphResult.EffectiveState()`
- `SubgraphResult.EffectiveStateDelta()`

`ExecutionErrorCollector.SubgraphOutputMapper()` 已经内置了这层处理。

## A2A 结构化错误

### Server 侧

如果你的 A2A server 需要把 agent 业务错误规范化地暴露给调用方，开启：

```go
server, err := a2aserver.New(
    a2aserver.WithHost("http://localhost:8080"),
    a2aserver.WithAgent(myAgent, true),
    a2aserver.WithStructuredTaskErrors(true),
)
```

开启后：

- unary 响应会返回 failed `Task`
- streaming 响应会返回 failed `TaskStatusUpdateEvent`
- 结构化错误字段会写入 task metadata

### Client 侧

`A2AAgent` 会自动识别这类结构化 task failure。

对于 failed、rejected、canceled 这些远端任务终态，它会重建成普通
`event.Event`，其中包含：

- `Response.Object = "error"`
- `Response.Error.Type`
- `Response.Error.Message`
- `Response.Error.Code`

在 streaming 模式下，`A2AAgent` 还会避免“先收到终态错误，再补一条正常 final
assistant message”的歧义行为。

## 哪些仍然属于业务侧

框架统一的是“传递机制”和“收集机制”，不是业务策略本身。

### 业务侧仍需自己决定的部分

- 哪些错误算 recoverable
- recover 之后应该走哪个 fallback 路径
- state key 用什么名字最合适
- 如何做错误聚合、去重、分组
- 第三方 A2A provider 的特殊 task state 怎么解释

### 推荐扩展点

- 用 `WithExecutionErrorPolicy(...)` 写恢复策略
- 组合自定义 parent output mapper 时，直接调用
  `collector.SubgraphStateUpdate(result)`
- 如果 fatal 路径上还要发别的业务状态，用
  `graph.EmitCustomStateDelta(...)`
- 如果第三方 A2A metadata 约定不一致，用自定义
  `a2aagent.A2AEventConverter`

## 示例

可运行示例：

- `examples/graph/error_handling`
- `examples/a2aagent/error_handling`

graph 示例展示 recoverable 和 fatal 两条路径，以及最终状态读取方式。

A2A 示例展示 server 侧结构化 task error 和 client 侧 `Response.Error`
重建效果。
