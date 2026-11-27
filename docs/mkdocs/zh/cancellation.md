# 取消机制与上下文传播

tRPC-Agent-Go 使用 Go 的 `context.Context` 传递停止工作的信号。取消是协作式的：
框架、模型适配器、工具和业务代码都需要观察传入的上下文。调用 `cancel()` 不会强制
终止 goroutine、撤销外部写入，也不能证明远端模型已经停止计算。

本文介绍取消控制、调度检查和回调替换上下文的行为。在框架的调度检查点，
如果当前有效上下文已经取消，就会拒绝工具或模型调用。这改变了早期不配置重试时
仍可能使用已取消上下文调用工具的行为。

## 如何请求取消

向 `Runner.Run` 传入可取消的上下文，在用户要求停止时调用对应的取消函数。
之后继续读取事件，直到通道关闭；仅退出读取循环不会请求取消，还可能阻塞事件生产者。
下面的 `r` 是已创建的 `runner.Runner`，示例在启动后立即发出停止请求：

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

events, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
if err != nil {
    return err
}

cancel()
for range events {
    // Drain events until the public output channel closes.
}
```

需要按请求 ID 控制时，在 `Run` 中传入 `agent.WithRequestID(requestID)`，再调用
`runner.ManagedRunner.Cancel(requestID)`。返回的布尔值表示是否找到并调用已登记的
取消函数，不表示工具已经退出或结果已经持久化。截止时间到期也会取消上下文，
此时错误是 `context.DeadlineExceeded`，而不是 `context.Canceled`。

请求 ID 的用法见 [Runner](runner.md)，服务端接口见 [AG-UI 取消路由](agui/cancel.md)。

## 后续执行由哪个上下文控制

“当前有效上下文”就是某个操作当前接收到的上下文。`BeforeModelResult.Context`
或 `BeforeToolResult.Context` 非空时，会替换对应回调及执行路径后续使用的上下文；
Context 为 nil 则保持不变。其他带 Context 字段的回调结果遵循各自约定的作用范围。
替换上下文不会改写调用方、兄弟任务或外层 Runner 已持有的上下文。

调度时使用的上下文如下：

| 时点 | 取消检查 |
| --- | --- |
| 接受新工作或执行回调之前 | 检查传入的上下文。 |
| 等待并发名额之后 | 检查当前上下文；若停止调度，释放已取得的名额。 |
| 回调和权限检查结束、即将进入执行时 | 检查当前有效上下文，包括回调显式返回的替换上下文。 |
| 调用工具、模型或开始新一次尝试之前 | 当前有效上下文已经取消时，拒绝执行。 |

不应仅为了取消检查保存旧的 `dispatchCtx`，并在回调替换上下文之后继续让它拥有
额外的否决权。这保留了回调替换上下文的语义，但不意味着取消检查与外部 I/O 是
原子操作，也不保证调度入口已经拒绝工作后还会执行回调。模型、工具以及 Graph
路径采用一致的规则。

按照此契约，回调取消输入上下文且不返回替换值时，下一次调用会被拒绝；从该输入
派生的子上下文也会被取消。显式脱离取消关系的新上下文则可能让相应后续调用继续。
这是业务选择，原上下文并没有恢复。

## 替换上下文的影响

| 回调返回的上下文 | 是否继承输入的取消和截止时间 | 是否保留输入的值 |
| --- | --- | --- |
| `ctx` 或不替换 | 是 | 是 |
| `context.WithValue(ctx, key, value)` | 是 | 是，并增加或覆盖指定 key |
| `context.WithCancel(ctx)` 创建的子上下文 | 是，也可独立取消 | 是 |
| `context.WithTimeout(ctx, duration)` 创建的子上下文 | 是，不能延长父上下文更早的截止时间 | 是 |
| `context.Background()` 或 `context.TODO()` | 否 | 否 |
| `context.WithoutCancel(ctx)` | 否，父上下文的截止时间也会被移除 | 是 |

这些行为来自 Go 的上下文语义，参见 [context 官方文档](https://pkg.go.dev/context)。

返回 Background/TODO 可能丢失输入上下文中携带的请求追踪、Invocation、工具调用
标识等框架信息。部分路径会补回特定信息，但不能假定所有元数据都会被恢复。
WithoutCancel 保留取值能力，却移除了取消和截止时间；在它之上创建新的可取消
子上下文之前，它的 Done 通道为 nil。

脱离取消只影响拿到新上下文的操作。外层 Runner 仍可能观察自己的取消信号并关闭
输出，因此不能推断它还会等待、收集或持久化这些操作的结果。业务需要负责这些
工作的超时、清理、结果处理及后台 goroutine 的生命周期。仅保留上下文的值，
并不能建立完整的后台执行生命周期。

## callback 中的推荐写法

### 添加请求信息时保留取消传播

基于回调传入的 `ctx` 派生上下文。使用业务自己的 key 类型，并只存放请求范围内的
数据。下面的注册代码假定已经导入 `context`、`model` 和 `tool`：

```go
type requestTagKey struct{}

modelCallbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        return &model.BeforeModelResult{
            Context: context.WithValue(ctx, requestTagKey{}, "checkout"),
        }, nil
    },
)

toolCallbacks := tool.NewCallbacks().RegisterBeforeTool(
    func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
        return &tool.BeforeToolResult{
            Context: context.WithValue(ctx, requestTagKey{}, "checkout"),
        }, nil
    },
)
```

通过 `llmagent.WithModelCallbacks(modelCallbacks)` 和
`llmagent.WithToolCallbacks(toolCallbacks)` 配置这些回调。如果不需要修改上下文，
返回 `nil, nil` 即可保留输入上下文。

### 只给回调自身的工作设置超时

如果只是回调中的权限查询或其他 I/O 需要更短的超时，为这段工作创建子上下文，
实际工具调用仍使用原来的上下文。下面的 `check` 是业务提供的
`func(context.Context) error`：

```go
toolCallbacks.RegisterBeforeTool(
    func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
        checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
        defer cancel()

        if err := check(checkCtx); err != nil {
            return nil, err
        }
        return nil, nil
    },
)
```

**不要在回调中 `defer cancel()`，同时把 `checkCtx` 作为 Context 返回给后续操作。**
回调一返回，defer 就会取消它，后面的工具或模型拿到的就是已取消上下文。
若超时需要覆盖后续执行，取消函数的所有者必须活得足够久。优先在整个 run 的外层
或实际拥有这段工作的操作内部设置超时，不要假定配对的 After 回调会在所有错误和
提前返回路径上运行。

### 普通工作保留取消，清理工作局部脱离并设置超时

解锁等本地资源释放通常不需要上下文。取消之后还要执行 I/O 的清理操作，可以使用
独立且有超时的上下文，但仅在清理内部使用，不要将它返回给普通执行链。
下面的 `cleanup` 是业务提供的 `func(context.Context) error`：

```go
toolCallbacks.RegisterAfterTool(
    func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
        cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
        defer cancel()

        if err := cleanup(cleanupCtx); err != nil {
            return nil, err
        }
        return nil, nil
    },
)
```

AfterTool 不是与 BeforeTool 配对的通用 `finally`。取消处理需要区分“调用被拒绝”
和“调用已经执行后返回”：

| 情况 | AfterTool 行为 |
| --- | --- |
| 进入调度前、等待并发名额时，或前置回调及权限检查后、第一次工具执行前，被取消检查拦截 | 返回取消错误，不进入 AfterTool。 |
| 前置回调返回错误或自定义结果，或权限检查拒绝、报错 | 保持原有提前返回行为，不保证执行配对的 AfterTool。 |
| 工具已经开始执行后返回错误（包括取消），或后续重试、流消费被取消 | 携带结果和错误，继续原有的 AfterTool 回调链。 |

插件覆盖结果或回调报错仍可能使回调链提前结束。仅凭错误值，无法区分“执行前被
取消拦截”和“工具开始执行后返回同一个取消错误”。AfterTool 也不表示工具执行成功；
依赖成功结果的业务操作需要检查它收到的结果和错误。

上面的例子仅适用于执行到 AfterTool 的情况。回调内部使用的资源，在该回调内部
用 `defer` 释放；贯穿工具调用的资源，通常应在工具内部或拥有该调用的包装层中
申请，并在那里用 `defer` 释放。不要在 BeforeTool 中申请资源后，假定 AfterTool
总能负责释放。确实需要跨回调持有资源时，其所有者必须覆盖所有提前退出路径。
示例会通过回调 API 返回清理错误，业务应按自己的错误处理要求决定如何上报。

## 让整个 run 在父上下文取消后继续

如果希望 HTTP 连接断开后整个 run 继续执行，优先使用已有的 Runner 选项，
不要只在某个回调中替换上下文：

```go
events, err := r.Run(
    ctx, "user", "session", model.NewUserMessage("prepare a report"),
    agent.WithRequestID("report-123"),
    agent.WithDetachedCancel(true),
    agent.WithMaxRunDuration(30*time.Second),
)
```

Runner 会脱离父上下文的取消，但重新施加“原父上下文截止时间”和 MaxRunDuration
中更早的限制。显式调用 ManagedRunner.Cancel 仍会取消已登记的执行上下文。
需要有调用方继续消费事件。这些控制能够传播到保留上下文父子关系的后续工作；
按选定的契约，之后的回调仍可能再次显式切断这个关系。

## 取消能够说明什么

需要区分：已请求取消、对外输出已关闭、工作实际结束、结果已得到存储确认。
前三者中的任意一个，都不能单独证明之后的所有阶段已经完成。#2757 中涉及的
执行观察、持久化和安全 Resume 是独立的草稿变更，不是上下文取消自带的保证。

对于 LLMFlow 使用合并结果路径的顺序工具批次，如果批次上下文本身已到达
`context.DeadlineExceeded`，且该超时导致后续调用在执行前被拒绝，前面已经完成的结果可以
进入现有的有界持久化回退流程。如果只有回调提供的子上下文超时，而批次上下文
仍有效，则不满足这个条件。这是一次存储尝试，不代表结果已经发送到对外事件流。
保留的消息在持久化前会经过 `AfterToolMessages`。
这个超时收尾阶段的钩子会收到保留原有值、重新设置一秒超时的上下文；随后追加
存储还有自己的有界超时。如果消息改写失败，不会持久化原始消息；已完成工具的
执行轨迹只保留身份及成功或失败信息，不保留参数和输出。持久化本身也可能失败。

例如，A 已完成、超时阻止 B 执行时，保留的批次包含 A 的结果，以及框架为 B
生成的工具消息：

```json
{"status":"not_executed","reason":"deadline_exceeded"}
```

后续每个尚未开始的调用都会得到同样的状态消息。这些消息与 A 的结果一起经过
`AfterToolMessages`，仍受业务消息改写逻辑约束。持久化成功后，下一轮模型请求
既能使用 A 的结果，也能知道 B 在本批次未执行；每个工具调用都有对应的工具消息，
原生提供方载荷保持不变。生成状态不会调用 B、触发其 `AfterTool`、收集其状态变更，
也不会安排重试。已经开始执行、随后返回取消错误的工具仍采用原有的错误结果语义，
不标记为未执行。该状态只描述本批次，不代表同一工具在更早调用中的执行结果。

显式取消不会启用这个回退流程：后续调用被拒绝时，该路径不会发送或持久化
已累积的合并结果。内部处理函数保留取消错误，但 `ProcessResponse` 不向公开
调用方返回 Go 错误。对外事件流可能包含带错误文本的错误事件，而非 Go 的
`context.Canceled` 或 `context.DeadlineExceeded` 哨兵错误值。上下文结束后，
该错误事件也不保证能够送达。

逐工具结果发布要求：启用对应选项、批次包含多个工具调用、没有延迟或外部执行的
调用，且没有 `LongRunning()` 返回 `true` 的 `LongRunner` 工具。满足条件时，
顺序和并行执行都会在各调用完成时分别发布结果，并采用独立的轮次处理逻辑；
否则使用合并结果路径。这些 LLMFlow 持久化规则不构成 Graph 检查点或 Resume 保证。

工具应在产生副作用前检查当前有效上下文，将其传给 HTTP、数据库等 I/O 接口，
并在长时间等待时观察 Done。已经运行且不响应上下文的代码可能继续执行。
外部写入重试前可能需要幂等键或结果查询。模型流取消不证明远端推理或计费停止。
替换上下文也不意味着可以重新执行结果未知的旧工具调用。

相关 API 参见 [Callbacks](callbacks.md)、[Runner](runner.md) 和
[错误处理](error-handling.md)。

## 可选的真实模型验证

普通测试不会调用付费模型。为兼容 OpenAI 的服务配置 `OPENAI_BASE_URL`、
`OPENAI_API_KEY` 和 `MODEL_NAME` 后，显式启用：

```sh
TRPC_CANCEL_LIVE=1 go test ./runner -run '^TestCancellationLive_' -v -count=1 -timeout=5m
```

探针工具的副作用仅为本地计数器。测试检查已取消的当前有效上下文会阻止工具和
AfterTool 执行，以及取消模型流之后输出会关闭；不证明远端计算终止、写入回滚或
停止计费。不要把凭证写入测试日志。
