# Error Handling

## 概述

本文定义 graph 工作流、Runner 结束态、subgraph 传播以及 A2A 传输场景下的标准错误处理模型。

Agent 应用里，错误处理通常同时需要两类信息：

- 给程序分支、重试、监控系统用的结构化错误信号
- 给业务排障、回放、上报系统用的稳定业务错误信息

在实际系统里，这些信息可能来自：

- 本地节点
- 子图 / sub-agent
- 远端 A2A agent

tRPC-Agent-Go 为这三条链路提供了一条统一路径。

## 设计目标

这套方案遵循四条规则：

1. `Response.Error` 继续作为事件流里的失败信号，终态判断请用
   `event.IsTerminalError()`。
2. 业务可见的错误集合沉淀在 graph state 中。
3. 可恢复错误继续执行，但不能丢记录。
4. 致命错误即使提前终止，也要先把 fallback 业务状态发出去。

## 适用场景

这套设计覆盖的是过去经常让业务团队单独维护节点错误包的那批核心诉求：

- 收集本地节点错误，包括 recoverable 错误
- 在运行结束后还能稳定拿到业务错误明细
- 把子图 / sub-agent 的错误回传到父图
- 让 Runner 在 `runner.completion` 上暴露 fatal 路径的 fallback state
- 把 A2A 的结构化 task failure 重新还原成 `Response.Error`

如果现有实现是“把节点错误沉淀到 graph state，再在运行结束后统一读取”，那么
`graph.ExecutionErrorCollector` 就是这个模式的框架标准实现。

## 文档结构

这页主要分成几部分：

1. “业务错误码如何管理”说明错误码模型和职责归属。
2. “推荐的 graph 用法”说明 graph 工作流里的接入方式。
3. “运行结束后怎么拿错误”说明 Runner 侧的统一消费模式。
4. subgraph 和 A2A 两节按需阅读，只在系统跨这些边界时看。

## 职责边界

框架负责的是传递、归一化、收集这几件机制层的事情。

框架负责统一：

- transport failure 放在哪里：`Response.Error`
- transport failure 何时算终态：`event.IsTerminalError()`
- 业务可见错误记录放在哪里：graph state
- fatal fallback state 如何传递到 `runner.completion`
- child fallback state 如何与正常 child completion 区分
- A2A 结构化 task failure 如何重新变成 `Response.Error`

业务侧需要自己决定：

- 有哪些错误码
- 哪些错误码算 recoverable
- recover 之后应该走哪个 fallback 路径
- 多条错误记录是否需要聚合、去重、分组
- 错误最终怎么持久化、告警、上报

框架不定义“全局业务错误码注册表”。它统一的是结构化错误码的承载和收集方式；错误码命名空间
本身仍然属于具体业务或领域。

## 业务错误码如何管理

框架支持错误码，但支持的是“承载和归一化机制”，不是“中心化业务错误码表”。

结论：

- 框架不接管你的业务错误码目录
- `model.ResponseError.Code` 的最终形态是 `string`
- 已有的整型错误码仍然支持，框架会自动转成十进制字符串
- 对新的业务错误，默认推荐稳定的字符串错误码

### `Code` 的表示形式

`model.ResponseError.Code` 在模型里定义的就是 `*string`。

这样设计是有意为之：

- 事件流和 A2A metadata 用字符串做跨边界传输更稳定
- 字符串更适合表达带命名空间的业务码，例如
  `ORDER_INVENTORY_SOFT_TIMEOUT`
- 跨语言、跨服务协作时，不需要再约定整型区间或枚举归属

如果现有系统已经有成熟的整型错误码体系，不需要为了接框架而改造。框架会在传输边界把它们转成
字符串。

### 框架识别的错误约定

默认情况下，`graph.NewExecutionError(...)` 会调用
`model.ResponseErrorFromError(err, model.ErrorTypeFlowError)`。

Go 不支持方法重载。下面这张表表达的是“框架会识别不同错误类型上可能采用的不同约定”，不是说
同一个具体类型要同时实现这些同名方法。

| 错误类型可选实现的方法 | 框架行为 |
| --- | --- |
| `ErrorType() string` | 填充 `ResponseError.Type` |
| `ErrorCode() string` | 直接填充 `ResponseError.Code` |
| `Code() string` | 直接填充 `ResponseError.Code` |
| `Code() int` | 转成十进制字符串后填充 |
| `Code() int32` | 转成十进制字符串后填充 |
| `Code() int64` | 转成十进制字符串后填充 |

### 新业务默认推荐模式

推荐模式是：

1. 业务侧用一个小的领域包定义稳定的字符串错误码常量。
2. 节点、工具、Agent 返回带类型的业务错误。
3. 让 collector 自动把这些错误码记录下来。
4. 让默认 collector 策略根据 `Recoverable() bool` 自动判断 recover。
5. `WithExecutionErrorPolicy(...)` 主要用于定制 fallback 路由，或做必要的归一化。

示例：业务错误包

```go
package ordererrors

import (
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
)

const (
    CodeInventorySoftTimeout = "ORDER_INVENTORY_SOFT_TIMEOUT"
    CodeInventoryUnavailable = "ORDER_INVENTORY_UNAVAILABLE"
)

type Error struct {
    code        string
    message     string
    recoverable bool
}

func (e *Error) Error() string {
    return e.message
}

func (e *Error) ErrorCode() string {
    return e.code
}

func (e *Error) ErrorType() string {
    return model.ErrorTypeFlowError
}

func (e *Error) Recoverable() bool {
    return e.recoverable
}

func NewInventorySoftTimeout(itemID string) error {
    return &Error{
        code:        CodeInventorySoftTimeout,
        message:     fmt.Sprintf(
            "inventory lookup timed out for %s",
            itemID,
        ),
        recoverable: true,
    }
}

func NewInventoryUnavailable(itemID string) error {
    return &Error{
        code:        CodeInventoryUnavailable,
        message:     fmt.Sprintf(
            "inventory service is unavailable for %s",
            itemID,
        ),
        recoverable: false,
    }
}
```

如果你们已经有遗留的整型错误码体系，也完全可以兼容：

```go
type legacyRPCError struct {
    code    int
    message string
}

func (e *legacyRPCError) Error() string {
    return e.message
}

func (e *legacyRPCError) Code() int {
    return e.code
}
```

这类错误最终会以类似 `"40401"` 这样的字符串形式写入 `ResponseError.Code`。

因为 `ordererrors.Error` 实现了 `Recoverable() bool`，所以默认 collector
策略已经会把 `NewInventorySoftTimeout(...)` 识别成 recoverable。

示例：保留默认判定，同时补一个自定义 fallback 路由

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = &graph.Command{
                Update: graph.State{
                    "inventory_status": "fallback",
                },
                GoTo: "fallback_lookup",
            }
            return policy
        }),
    )
}
```

如果你的内部错误比较杂，或者被第三方库层层包装，可以用
`ExecutionErrorPolicy.ResponseError` 把它们先归一化成统一的业务错误形态，再存入
collector。

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

`graph.NewExecutionErrorCollector()` 现在自带一套保守的默认策略：

- 错误实现了 `Recoverable() bool` 且返回 `true`
- 错误被 `graph.MarkRecoverable(err)` 包装，或直接用
  `graph.NewRecoverableError(...)` 创建

示例：

```go
package main

import (
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

type quotaSoftLimitError struct {
    message string
}

func (e quotaSoftLimitError) Error() string {
    return e.message
}

func (e quotaSoftLimitError) Recoverable() bool {
    return true
}

func lookupQuota() error {
    return quotaSoftLimitError{
        message: "quota service returned soft limit",
    }
}

func lookupCache() error {
    return graph.NewRecoverableError("cache lookup timed out")
}
```

如果你要在这个基础上再补充一些 recoverable 规则，可以使用
`graph.WithRecoverableExecutionErrors(...)`：

```go
package main

import (
    "errors"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

var errQuotaSoftLimit = errors.New("quota soft limit")

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithRecoverableExecutionErrors(func(err error) bool {
            return errors.Is(err, errQuotaSoftLimit)
        }),
    )
}
```

当 `Recover` 为 `true` 时，collector 会：

- 写入一条 `recoverable` 记录
- 保持 graph 继续执行

### 4. 恢复时可选自定义 replacement

如果可恢复错误需要带着自定义状态更新或跳转继续，可以使用
`ExecutionErrorPolicy.Replacement`。

推荐 replacement 类型：

- `graph.State`
- `*graph.Command`

如果 `Replacement` 是空的，collector 会优先保留原始的 `graph.State` 或
`*graph.Command` 结果，再把 `execution_errors` 合并进去。

如果你既想保留默认 recoverable 判定，又想做自定义 replacement，可以从
`graph.DefaultExecutionErrorPolicy(...)` 开始：

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func newCollector() *graph.ExecutionErrorCollector {
    return graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = &graph.Command{
                Update: graph.State{
                    "cache_status": "miss",
                },
                GoTo: "fallback_builder",
            }
            return policy
        }),
    )
}
```

### 5. 完整的 graph 接入示例

如果你希望有一段可以直接照着落地的标准 graph 接入参考，可以从下面这个形态开始：

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

const codeInventorySoftTimeout = "ORDER_INVENTORY_SOFT_TIMEOUT"

type inventoryError struct {
    code        string
    message     string
    recoverable bool
}

func (e *inventoryError) Error() string {
    return e.message
}

func (e *inventoryError) ErrorCode() string {
    return e.code
}

func (e *inventoryError) ErrorType() string {
    return model.ErrorTypeFlowError
}

func (e *inventoryError) Recoverable() bool {
    return e.recoverable
}

func buildAgent() (agent.Agent, error) {
    collector := graph.NewExecutionErrorCollector(
        graph.WithExecutionErrorPolicy(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            state graph.State,
            result any,
            err error,
        ) graph.ExecutionErrorPolicy {
            policy := graph.DefaultExecutionErrorPolicy(
                ctx,
                cb,
                state,
                result,
                err,
            )
            if !policy.Recover {
                return policy
            }
            policy.Replacement = graph.State{
                "inventory_status": "fallback",
            }
            return policy
        }),
    )

    schema := graph.MessagesStateSchema()
    collector.AddField(schema)

    sg := graph.NewStateGraph(schema).
        WithNodeCallbacks(collector.NodeCallbacks())

    sg.AddNode("lookup_inventory", func(
        ctx context.Context,
        state graph.State,
    ) (any, error) {
        return nil, &inventoryError{
            code:        codeInventorySoftTimeout,
            message:     "inventory lookup timed out",
            recoverable: true,
        }
    })

    sg.AddNode("finalize", func(
        ctx context.Context,
        state graph.State,
    ) (any, error) {
        return graph.State{
            "done": true,
        }, nil
    })

    compiled, err := sg.
        AddEdge("lookup_inventory", "finalize").
        SetEntryPoint("lookup_inventory").
        SetFinishPoint("finalize").
        Compile()
    if err != nil {
        return nil, err
    }
    return graphagent.New("inventory-agent", compiled)
}
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
把 fallback 业务状态复制到最后那条 `runner.completion` 上，并把最终的
`Response.Error` 也挂到这条 completion 上。

因此应用层可以用一个统一规则：

- 一直消费到 `runner.completion`
- 从它的 `StateDelta` 里读取 collector key
- 终态 transport failure 用 `event.IsTerminalError()` 判断，再读
  `Response.Error`

完整的 Runner 侧消费模式：

```go
package main

import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

type RunSummary struct {
    TransportError  *model.ResponseError
    ExecutionErrors []graph.ExecutionError
}

func ConsumeUntilCompletion(
    ctx context.Context,
    events <-chan *event.Event,
) (*RunSummary, error) {
    summary := &RunSummary{}

    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case evt, ok := <-events:
            if !ok {
                return summary, nil
            }
            if evt.IsTerminalError() &&
                evt.Response != nil {
                summary.TransportError = evt.Response.Error
            }
            if !evt.IsRunnerCompletion() {
                continue
            }

            executionErrors, err := graph.ExecutionErrorsFromStateDelta(
                evt.StateDelta,
                graph.StateKeyExecutionErrors,
            )
            if err != nil {
                return nil, err
            }
            summary.ExecutionErrors = executionErrors
            return summary, nil
        }
    }
}

func PrintSummary(summary *RunSummary) {
    if summary.TransportError != nil {
        fmt.Printf(
            "transport error: type=%s code=%s message=%s\n",
            summary.TransportError.Type,
            ptrValue(summary.TransportError.Code),
            summary.TransportError.Message,
        )
    }
    for _, record := range summary.ExecutionErrors {
        if record.Error == nil {
            continue
        }
        fmt.Printf(
            "execution error: severity=%s node=%s code=%s message=%s\n",
            record.Severity,
            record.NodeName,
            ptrValue(record.Error.Code),
            record.Error.Message,
        )
    }
}

func ptrValue(value *string) string {
    if value == nil {
        return ""
    }
    return *value
}
```

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

如果你除了子错误回传之外，还需要在父图附带额外业务状态，可以围绕
`collector.SubgraphStateUpdate(result)` 组合自己的 mapper：

```go
package main

import "trpc.group/trpc-go/trpc-agent-go/graph"

func parentOutputMapper(
    collector *graph.ExecutionErrorCollector,
) graph.SubgraphOutputMapper {
    return func(
        parent graph.State,
        result graph.SubgraphResult,
    ) graph.State {
        update := collector.SubgraphStateUpdate(result)
        if update == nil {
            update = graph.State{}
        }
        update["child_status"] = "degraded"
        return update
    }
}
```

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
- 只有终态错误会转换成 failed task；`graph.node.error` 这类中间事件仍然按
  graph 事件继续透传

这里的 payload 是按两层职责来组织的：

- 外层 `Task.Metadata` 或 `TaskStatusUpdateEvent.Metadata`：推荐优先读取的
  机器可读结构化错误字段，例如 `error_type`、`error_code`、
  `error_message`、`task_state`、`llm_response_id`
- `Status.Message.Metadata`：为了兼容 A2A interaction spec `0.1`，继续镜像
  同一份机器可读字段
- `Status.Message.Parts`：给人直接展示的文本

一个 streaming 终态失败的 shape 大致如下：

```json
{
  "kind": "status-update",
  "metadata": {
    "object_type": "error",
    "error_type": "flow_error",
    "error_code": "REMOTE_VALIDATION_FAILED",
    "error_message": "validation failed",
    "task_state": "failed",
    "llm_response_id": "resp-1"
  },
  "status": {
    "state": "failed",
    "message": {
      "messageId": "resp-1",
      "role": "agent",
      "metadata": {
        "object_type": "error",
        "error_type": "flow_error",
        "error_code": "REMOTE_VALIDATION_FAILED",
        "error_message": "validation failed",
        "task_state": "failed",
        "llm_response_id": "resp-1"
      },
      "parts": [
        {
          "kind": "text",
          "text": "validation failed"
        }
      ]
    }
  }
}
```

因此业务侧可以用一条稳定规则处理：

- 用 `status.state` 判断任务终态
- 优先用外层 metadata 读取结构化错误字段
- `status.message.metadata` 只作为兼容旧客户端的镜像字段
- `status.message.parts` 只作为展示文本，不作为机器判断的主来源

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

换句话说，默认 client 路径已经遵循同一套规则：

- 外层 metadata 是重建 `Response.Error` 的首选来源
- `status.message.metadata` 继续作为 `0.1` 的兼容镜像
- `status.message.parts` 只是给人读的兜底文本通道
- 业务代码继续基于 `evt.Response.Error` 分支，而不是解析文本

完整的 server / client 接入方式：

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
    a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

func buildA2AServer(myAgent agent.Agent) error {
    _, err := a2aserver.New(
        a2aserver.WithHost("127.0.0.1:18888"),
        a2aserver.WithAgent(myAgent, true),
        a2aserver.WithStructuredTaskErrors(true),
    )
    return err
}

func buildA2AClient() (agent.Agent, error) {
    return a2aagent.New(
        a2aagent.WithAgentCardURL("http://127.0.0.1:18888"),
        a2aagent.WithEnableStreaming(true),
    )
}
```

如果你对接的是一个 metadata 约定不同的第三方 A2A provider，业务侧应该在 converter
层做扩展：

- 保持框架统一错误模型仍然是 `Response.Error`
- 实现 `a2aagent.A2AEventConverter`
- 用 `a2aagent.WithCustomEventConverter(...)` 注册

这是 provider 适配最合适的位置。不要在转换之后再额外重新发明第二套错误传输格式。

## 推荐的职责划分

最清晰的生产实践通常是：

- 框架：收集、传递、序列化、暴露结构化错误
- 业务错误包：定义错误码常量和 typed error
- graph policy：决定 recoverable 还是 fatal
- runner 消费侧：从 `runner.completion` 持久化 `ExecutionErrors`
- transport 消费侧：用 `event.IsTerminalError()` 和 `Response.Error`
  处理终态失败

这个划分已经足够替代旧的业务侧 node-error helper，同时又不会越界接管你的领域错误码体系。

## 示例

可运行示例：

- `examples/graph/error_handling`
- `examples/a2aagent/error_handling`

graph 示例展示 recoverable 和 fatal 两条路径，以及最终状态读取方式。

A2A 示例展示 server 侧结构化 task error 和 client 侧 `Response.Error`
重建效果。
