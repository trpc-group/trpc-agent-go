## 回调（Callbacks）

> **版本要求**  
> 结构化回调 API（推荐）需要 **trpc-agent-go >= 0.6.0**。

本文介绍项目中的回调系统，用于拦截、观测与定制模型推理、工具调用、Agent 执行以及 AG-UI 事件翻译。

回调分为四类：

- ModelCallbacks（模型回调）
- ToolCallbacks（工具回调）
- AgentCallbacks（Agent 回调）
- TranslateCallbacks（AGUI 事件翻译回调）

每类都有 Before 与 After 两种回调。Before 回调可以通过返回非空结果提前返回，跳过默认执行。

所有回调将按照注册顺序依次执行，且一旦有回调返回非 `nil` 值，回调链路将立即停止。如果需要叠加多个回调效果，请在单一回调中实现相应的逻辑。

---

## ModelCallbacks

### 结构化模型回调（推荐）

- BeforeModelCallbackStructured：模型推理前触发，使用结构化参数
- AfterModelCallbackStructured：模型完成后触发，使用结构化参数

参数：

```go
type BeforeModelArgs struct {
    Request *model.Request  // 即将发送的请求（可修改）
}

type BeforeModelResult struct {
    Context        context.Context  // 可选，用于后续操作的上下文
    CustomResponse *model.Response  // 非空时跳过模型调用并返回此响应
}

type AfterModelArgs struct {
    Request  *model.Request   // 发送给模型的原始请求
    Response *model.Response  // 模型返回的响应（可能为 nil）
    Error    error            // 模型调用过程中发生的错误
}

type AfterModelResult struct {
    Context        context.Context  // 可选，用于后续操作的上下文
    CustomResponse *model.Response  // 非空时替换原始响应
}
```

签名：

```go
type BeforeModelCallbackStructured func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error)
type AfterModelCallbackStructured  func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error)
```

要点：

- 结构化参数提供更好的类型安全性和更清晰的意图
- `BeforeModelResult.Context` 可用于向后续操作传递上下文
- `AfterModelResult.Context` 允许在回调之间传递上下文
- Before 可返回非空响应以跳过模型调用
- After 可获取原始请求 `req`，便于内容还原与后处理

### 回调执行控制

默认情况下，回调执行会在以下情况立即停止：

- 回调返回错误
- 回调返回非空的 `CustomResponse`（对于 Before 回调）或 `CustomResult`（对于 Tool 回调）

你可以在创建回调时使用选项来控制此行为：

```go
// 即使发生错误也继续执行剩余的回调
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnError(true),
)

// 即使返回了 CustomResponse 也继续执行剩余的回调
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnResponse(true),
)

// 启用两个选项：在错误和 CustomResponse 时都继续执行
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnError(true),
    model.WithContinueOnResponse(true),
)
```

**执行模式：**

1. **默认（两者均为 false）**：遇到第一个错误或 CustomResponse 时停止
2. **继续执行（错误）**：即使某个回调返回错误，也继续执行剩余的回调
3. **继续执行（响应）**：即使某个回调返回了 CustomResponse，也继续执行剩余的回调
4. **继续执行（两者）**：无论发生错误或返回 CustomResponse，都继续执行所有回调

**优先级规则：**

- 如果同时发生错误和 CustomResponse，错误优先，将被返回（除非 `continueOnError` 为 true）
- 当 `continueOnError` 为 true 且发生错误时，执行会继续，但第一个错误会被保留并在最后返回
- 当 `continueOnResponse` 为 true 且返回了 CustomResponse 时，执行会继续，但最后一个 CustomResponse 会被使用

示例：

```go
modelCallbacks := model.NewCallbacks().
  // Before：对特定提示直接返回固定响应，跳过真实模型调用
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    if len(args.Request.Messages) > 0 && strings.Contains(args.Request.Messages[len(args.Request.Messages)-1].Content, "/ping") {
      return &model.BeforeModelResult{
        CustomResponse: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "pong"}}}},
      }, nil
    }
    return nil, nil
  }).
  // After：在成功时追加提示信息，或在出错时包装错误信息
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    if args.Error != nil {
      return nil, args.Error
    }
    if args.Response != nil && len(args.Response.Choices) > 0 {
      args.Response.Choices[0].Message.Content += "\n\n-- answered by callback"
      return &model.AfterModelResult{CustomResponse: args.Response}, nil
    }
    return nil, nil
  })
```

**使用方式**：创建 callbacks 后，需要在创建 LLM Agent 时通过 `llmagent.WithModelCallbacks()` 选项传入：

```go
// 创建模型回调
modelCallbacks := model.NewCallbacks().
  RegisterBeforeModel(...).
  RegisterAfterModel(...)

// 创建 LLM Agent 并传入模型回调
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithModelCallbacks(modelCallbacks),  // 传入模型回调
)
```

完整示例请参考 [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go)。

### 传统模型回调（已弃用）

> **⚠️ 已弃用**
> 传统回调已弃用。请在新代码中使用结构化回调。

---

## ToolCallbacks

### 结构化工具回调（推荐）

- BeforeToolCallbackStructured：工具调用前触发，使用结构化参数
- AfterToolCallbackStructured：工具调用后触发，使用结构化参数

参数：

```go
type BeforeToolArgs struct {
    ToolName     string               // 工具名称
    Declaration  *tool.Declaration    // 工具声明元数据
    Arguments    []byte               // JSON 参数（可修改）
}

type BeforeToolResult struct {
    Context       context.Context     // 可选，用于后续操作的上下文
    CustomResult  any                 // 非空时跳过工具执行并返回此结果
    ModifiedArguments []byte          // 可选，修改后的参数用于工具执行
}

type AfterToolArgs struct {
    ToolName     string               // 工具名称
    Declaration  *tool.Declaration    // 工具声明元数据
    Arguments    []byte               // 原始 JSON 参数
    Result       any                  // 工具执行结果（可能为 nil）
    Error        error                // 工具执行过程中发生的错误
}

type AfterToolResult struct {
    Context       context.Context     // 可选，用于后续操作的上下文
    CustomResult  any                 // 非空时替换原始结果
}
```

签名：

```go
type BeforeToolCallbackStructured func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error)
type AfterToolCallbackStructured  func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error)
```

要点：

- 结构化参数提供更好的类型安全性和更清晰的意图
- `BeforeToolResult.ModifiedArguments` 允许修改工具参数
- `BeforeToolResult.Context` 和 `AfterToolResult.Context` 可在操作之间传递上下文
- 可通过 `args.Arguments` 直接修改参数
- BeforeToolCallbackStructured 返回非空自定义结果时，会跳过工具执行并直接使用该结果

### 回调执行控制

默认情况下，回调执行会在以下情况立即停止：

- 回调返回错误
- 回调返回非空的 `CustomResult`

你可以在创建回调时使用选项来控制此行为：

```go
// 即使发生错误也继续执行剩余的回调
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnError(true),
)

// 即使返回了 CustomResult 也继续执行剩余的回调
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnResponse(true),
)

// 启用两个选项：在错误和 CustomResult 时都继续执行
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnError(true),
    tool.WithContinueOnResponse(true),
)
```

**执行模式：**

1. **默认（两者均为 false）**：遇到第一个错误或 CustomResult 时停止
2. **继续执行（错误）**：即使某个回调返回错误，也继续执行剩余的回调
3. **继续执行（响应）**：即使某个回调返回了 CustomResult，也继续执行剩余的回调
4. **继续执行（两者）**：无论发生错误或返回 CustomResult，都继续执行所有回调

**优先级规则：**

- 如果同时发生错误和 CustomResult，错误优先，将被返回（除非 `continueOnError` 为 true）
- 当 `continueOnError` 为 true 且发生错误时，执行会继续，但第一个错误会被保留并在最后返回
- 当 `continueOnResponse` 为 true 且返回了 CustomResult 时，执行会继续，但最后一个 CustomResult 会被使用

示例：

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.Arguments != nil && args.ToolName == "calculator" {
      // 丰富参数
      original := string(args.Arguments)
      enriched := []byte(fmt.Sprintf(`{"original":%s,"ts":%d}`, original, time.Now().Unix()))
      args.Arguments = enriched
    }
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    if args.Error != nil {
      return nil, args.Error
    }
    if s, ok := args.Result.(string); ok {
      return &tool.AfterToolResult{
        CustomResult: s + "\n-- post processed by tool callback",
      }, nil
    }
    return nil, nil
  })
```

**使用方式**：创建 callbacks 后，需要在创建 LLM Agent 时通过 `llmagent.WithToolCallbacks()` 选项传入：

```go
// 创建工具回调
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(...).
  RegisterAfterTool(...)

// 创建 LLM Agent 并传入工具回调
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithTools(tools),
  llmagent.WithToolCallbacks(toolCallbacks),  // 传入工具回调
)
```

完整示例请参考 [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go)。

可观测与事件：

- 修改后的参数会同步到：
  - `TraceToolCall` 可观测属性
  - 图事件 `emitToolStartEvent` 与 `emitToolCompleteEvent`

### 传统工具回调（已弃用）

> **⚠️ 已弃用**
> 传统回调已弃用。请在新代码中使用结构化回调。

---

## AgentCallbacks

### 结构化 Agent 回调（推荐）

- BeforeAgentCallbackStructured：Agent 执行前触发，使用结构化参数
- AfterAgentCallbackStructured：Agent 执行后触发，使用结构化参数

参数：

```go
type BeforeAgentArgs struct {
    Invocation *agent.Invocation  // 调用上下文
}

type BeforeAgentResult struct {
    Context        context.Context  // 可选，用于后续操作的上下文
    CustomResponse *model.Response  // 非空时跳过 Agent 执行并返回此响应
}

type AfterAgentArgs struct {
    Invocation        *agent.Invocation  // 调用上下文
    FullResponseEvent *event.Event       // Agent 执行后的最终响应事件（可能为 nil）
    Error             error              // Agent 执行过程中发生的错误（可能为 nil）
}

type AfterAgentResult struct {
    Context        context.Context  // 可选，用于后续操作的上下文
    CustomResponse *model.Response  // 非空时替换原始响应
}
```

签名：

```go
type BeforeAgentCallbackStructured func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error)
type AfterAgentCallbackStructured  func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error)
```

要点：

- 结构化参数提供更好的类型安全性和更清晰的意图
- `BeforeAgentResult.Context` 和 `AfterAgentResult.Context` 可在操作之间传递上下文
- 可访问完整的调用上下文，便于实现丰富的按次逻辑
- Before 可返回自定义 `*model.Response` 以中止后续模型调用
- After 可返回替换响应
- `AfterAgentArgs.FullResponseEvent` 提供对 agent 最终输出结果的访问，可用于日志记录、监控、后处理等场景

### 回调执行控制

默认情况下，回调执行会在以下情况立即停止：

- 回调返回错误
- 回调返回非空的 `CustomResponse`

你可以在创建回调时使用选项来控制此行为：

```go
// 即使发生错误也继续执行剩余的回调
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnError(true),
)

// 即使返回了 CustomResponse 也继续执行剩余的回调
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnResponse(true),
)

// 启用两个选项：在错误和 CustomResponse 时都继续执行
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnError(true),
    agent.WithContinueOnResponse(true),
)
```

**执行模式：**

1. **默认（两者均为 false）**：遇到第一个错误或 CustomResponse 时停止
2. **继续执行（错误）**：即使某个回调返回错误，也继续执行剩余的回调
3. **继续执行（响应）**：即使某个回调返回了 CustomResponse，也继续执行剩余的回调
4. **继续执行（两者）**：无论发生错误或返回 CustomResponse，都继续执行所有回调

**优先级规则：**

- 如果同时发生错误和 CustomResponse，错误优先，将被返回（除非 `continueOnError` 为 true）
- 当 `continueOnError` 为 true 且发生错误时，执行会继续，但第一个错误会被保留并在最后返回
- 当 `continueOnResponse` 为 true 且返回了 CustomResponse 时，执行会继续，但最后一个 CustomResponse 会被使用

示例：

```go
agentCallbacks := agent.NewCallbacks().
  // Before：当用户消息包含 /abort 时，直接返回固定响应，跳过后续流程
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    if args.Invocation != nil && strings.Contains(args.Invocation.GetUserMessageContent(), "/abort") {
      return &agent.BeforeAgentResult{
        CustomResponse: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "aborted by callback"}}}},
      }, nil
    }
    return nil, nil
  }).
  // After：在成功响应末尾追加标注，可以访问 FullResponseEvent 获取最终响应事件
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
      return nil, args.Error
    }
    // 可以通过 FullResponseEvent 访问 agent 的最终输出结果
    if args.FullResponseEvent != nil && args.FullResponseEvent.Response != nil {
      if len(args.FullResponseEvent.Response.Choices) > 0 {
        c := args.FullResponseEvent.Response.Choices[0]
        c.Message.Content = c.Message.Content + "\n\n-- handled by agent callback"
        args.FullResponseEvent.Response.Choices[0] = c
        return &agent.AfterAgentResult{CustomResponse: args.FullResponseEvent.Response}, nil
      }
    }
    return nil, nil
  })
```

**使用方式**：创建 callbacks 后，需要在创建 LLM Agent 时通过 `llmagent.WithAgentCallbacks()` 选项传入：

```go
// 创建 Agent 回调
agentCallbacks := agent.NewCallbacks().
  RegisterBeforeAgent(...).
  RegisterAfterAgent(...)

// 创建 LLM Agent 并传入 Agent 回调
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithAgentCallbacks(agentCallbacks),  // 传入 Agent 回调
)
```

### 在回调中停止 agent {#stop-agent-via-callbacks}

当需要终止执行并向 runner 事件流发出 `stop_agent_error` 时，在回调中返回 `agent.NewStopError`。适用于配额校验、风控、人工中断等场景。

```go
agentCallbacks := agent.NewCallbacks().
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    if args.Invocation != nil && args.Invocation.TokenUsage.Total >= maxTokens {
      return nil, agent.NewStopError("token limit reached")
    }
    return nil, nil
  }).
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
      return nil, args.Error
    }
    if args.FullResponseEvent != nil && args.FullResponseEvent.Response != nil &&
      args.FullResponseEvent.Response.Usage.TotalTokens >= maxTokens {
      return nil, agent.NewStopError("token limit reached after response")
    }
    return nil, nil
  })
```

说明：

- Flow 会把 `StopError` 转换成 `stop_agent_error` 事件并停止循环。下游可以通过 `event.Error.Type == agent.ErrorTypeStopAgentError` 识别。
- 若需要对正在进行的模型或工具调用做硬截止，请同时使用 context 取消；关于上下文取消的模式见 runner 文档的中断说明。

完整示例请参考 [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go)。

### 传统 Agent 回调（已弃用）

> **⚠️ 已弃用**
> 传统回调已弃用。请在新代码中使用结构化回调。

---

## TranslateCallbacks

TranslateCallbacks 作为 AG-UI 事件 translate 的回调，分为两类：

- BeforeTranslateCallback：内部事件翻译为 AG-UI 事件之前触发
- AfterTranslateCallback：AG-UI 事件翻译完成，发往客户端前触发

签名：

```go
import (
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    "trpc.group/trpc-go/trpc-agent-go/event"
)

type BeforeTranslateCallback func(ctx context.Context, evt *event.Event) (*event.Event, error)
type AfterTranslateCallback  func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error)
```

要点：

- Before 回调返回非空自定义事件时，事件翻译的输入被替换为该自定义事件
- After 回调返回非空自定义事件时，事件翻译的输出被替换为该自定义事件，最终发送给客户端
- Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

示例：

```go
import (
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

translateCallbacks := translator.NewCallbacks().
  // 观测内部事件
  RegisterBeforeTranslate(func(ctx context.Context, event *event.Event) (*event.Event, error) {
    fmt.Println(event)
    return nil, nil
  }).
  // 观测 AG-UI 事件
  RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
    fmt.Println(event)
    return nil, nil
  })
```

TranslateCallbacks 的详细介绍参见 [agui](./agui.md)

---

## 在回调中访问 Invocation

回调可通过 context 获取当前的 Invocation 以便做关联日志、追踪或按次逻辑。

```go
if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
  fmt.Printf("invocation id=%s, agent=%s\n", inv.InvocationID, inv.AgentName)
}
```

示例工程在 Before/After 回调中打印了 Invocation 的存在性。

---

## Invocation State：在回调间共享状态

`Invocation` 提供了通用的 `State` 机制，用于存储 invocation 级别的状态数据。它不仅可以在 Before 和 After 回调之间共享数据，也可以用于中间件、自定义逻辑等场景。

### 核心方法

```go
// 设置状态值
func (inv *Invocation) SetState(key string, value any)

// 获取状态值，返回值和是否存在
func (inv *Invocation) GetState(key string) (any, bool)

// 删除状态值
func (inv *Invocation) DeleteState(key string)
```

### 特点

- **Invocation 级作用域**：状态自动限定在单次 Invocation 内
- **线程安全**：内置 RWMutex 保护，支持并发访问
- **懒初始化**：首次使用时才分配内存，节省资源
- **清晰生命周期**：使用完毕后可显式删除，避免内存泄漏
- **通用性强**：不限于 callbacks，可用于任何 invocation 级别的状态存储

### 命名约定

为避免不同使用场景之间的键冲突，建议使用前缀：

- Agent 回调：`"agent:xxx"`（如 `"agent:start_time"`）
- Model 回调：`"model:xxx"`（如 `"model:start_time"`）
- Tool 回调：`"tool:<toolName>:<toolCallID>:xxx"`（如 `"tool:calculator:call_abc123:start_time"`）
  - 注意：Tool 回调需要包含 tool call ID 以支持并发调用
- 中间件：`"middleware:xxx"`（如 `"middleware:request_id"`）
- 自定义逻辑：`"custom:xxx"`（如 `"custom:user_context"`）

### 示例：Agent 回调计时

```go
agentCallbacks := agent.NewCallbacks().
  // BeforeAgentCallback：记录开始时间
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    args.Invocation.SetState("agent:start_time", time.Now())
    return nil, nil
  }).
  // AfterAgentCallback：计算执行时长
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if startTimeVal, ok := args.Invocation.GetState("agent:start_time"); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Agent execution took: %v\n", duration)
      args.Invocation.DeleteState("agent:start_time") // 清理状态
    }
    return nil, nil
  })
```

### 示例：Model 回调计时

Model 和 Tool 回调需要先从 context 中获取 Invocation：

```go
modelCallbacks := model.NewCallbacks().
  // BeforeModelCallback：记录开始时间
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      inv.SetState("model:start_time", time.Now())
    }
    return nil, nil
  }).
  // AfterModelCallback：计算执行时长
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      if startTimeVal, ok := inv.GetState("model:start_time"); ok {
        startTime := startTimeVal.(time.Time)
        duration := time.Since(startTime)
        fmt.Printf("Model inference took: %v\n", duration)
        inv.DeleteState("model:start_time") // 清理状态
      }
    }
    return nil, nil
  })
```

### 示例：Tool 回调计时（支持并发工具调用）

当 LLM 在一次响应中返回多个工具调用（包括同一工具的多次调用）时，框架会并发执行这些工具。为了正确追踪每个工具调用的状态，需要使用 **tool call ID** 来区分：

```go
toolCallbacks := tool.NewCallbacks().
  // BeforeToolCallback：记录工具开始时间
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      // 获取 tool call ID 以支持并发调用
      toolCallID, ok := tool.ToolCallIDFromContext(ctx)
      if !ok || toolCallID == "" {
        toolCallID = "default" // 降级方案
      }

      // 使用 tool call ID 构建唯一键
      key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
      inv.SetState(key, time.Now())
    }
    return nil, nil
  }).
  // AfterToolCallback：计算工具执行时长
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      // 使用相同的逻辑获取 tool call ID
      toolCallID, ok := tool.ToolCallIDFromContext(ctx)
      if !ok || toolCallID == "" {
        toolCallID = "default"
      }

      key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
      if startTimeVal, ok := inv.GetState(key); ok {
        startTime := startTimeVal.(time.Time)
        duration := time.Since(startTime)
        fmt.Printf("Tool %s (call %s) took: %v\n", args.ToolName, toolCallID, duration)
        inv.DeleteState(key) // 清理状态
      }
    }
    return nil, nil
  })
```

**关键点**：

1. **获取 tool call ID**：使用 `tool.ToolCallIDFromContext(ctx)` 从 context 中获取唯一的工具调用 ID
2. **键名格式**：`"tool:<toolName>:<toolCallID>:<key>"` 确保并发调用的状态隔离
3. **降级处理**：如果获取不到 tool call ID（旧版本或特殊场景），使用 `"default"` 作为降级
4. **一致性**：Before 和 After 回调必须使用相同的逻辑获取 tool call ID

这样可以确保当 LLM 同时调用 `calculator` 多次（如 `calculator(1,2)` 和 `calculator(3,4)`）时，每个调用都有独立的计时数据。

### 完整示例

完整的计时示例（包含 OpenTelemetry 集成）请参考：
[examples/callbacks/timer](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/timer)

用户认证与授权示例（使用 Invocation State 进行权限检查和审计日志）请参考：
[examples/callbacks/auth](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/auth)

---

## 全局回调与链式注册

可通过链式注册构建可复用的全局回调配置。

```go
_ = model.NewCallbacks().
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    fmt.Printf("Global BeforeModel: %d messages.\n", len(args.Request.Messages))
    return nil, nil
  }).
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    fmt.Println("Global AfterModel: completed.")
    return nil, nil
  })

_ = tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    fmt.Printf("Global BeforeTool: %s.\n", args.ToolName)
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    fmt.Printf("Global AfterTool: %s done.\n", args.ToolName)
    return nil, nil
  })

_ = agent.NewCallbacks().
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    fmt.Printf("Global BeforeAgent: %s.\n", args.Invocation.AgentName)
    return nil, nil
  }).
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    fmt.Println("Global AfterAgent: completed.")
    return nil, nil
  })
```

---

## Mock 与参数修改示例

Mock 工具结果并中止后续工具调用：

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.ToolName == "calculator" && args.Arguments != nil && strings.Contains(string(args.Arguments), "42") {
      return &tool.BeforeToolResult{
        CustomResult: calculatorResult{Operation: "custom", A: 42, B: 42, Result: 4242},
      }, nil
    }
    return nil, nil
  })
```

执行前修改参数（并在可观测/事件中体现）：

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.Arguments != nil && args.ToolName == "calculator" {
      originalArgs := string(args.Arguments)
      modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`, originalArgs, time.Now().Unix())
      args.Arguments = []byte(modifiedArgs)
    }
    return nil, nil
  })
```

以上示例与 [`examples/callbacks`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks) 可运行示例保持一致。

---

## 运行示例

```bash
cd examples/callbacks
export OPENAI_API_KEY="your-api-key"

# 基本运行
go run .

# 指定模型
go run . -model gpt-4o-mini

# 关闭流式
go run . -streaming=false
```

可在日志中观察 Before/After 回调、参数修改与工具返回信息。
