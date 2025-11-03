## 回调（Callbacks）

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

- BeforeModelCallback：模型推理前触发
- AfterModelCallback：模型完成后触发（或按流式阶段）

签名：

```go
type BeforeModelCallback func(ctx context.Context, req *model.Request) (*model.Response, error)
type AfterModelCallback  func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error)
```

要点：

- Before 可返回非空响应以跳过模型调用
- After 可获取原始请求 `req`，便于内容还原与后处理
- Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

示例：

```go
modelCallbacks := model.NewCallbacks().
  // Before：对特定提示直接返回固定响应，跳过真实模型调用
  RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    if len(req.Messages) > 0 && strings.Contains(req.Messages[len(req.Messages)-1].Content, "/ping") {
      return &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "pong"}}}}, nil
    }
    return nil, nil
  }).
  // After：在成功时追加提示信息，或在出错时包装错误信息
  RegisterAfterModel(func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
    if runErr != nil || resp == nil || len(resp.Choices) == 0 {
      return resp, runErr
    }
    c := resp.Choices[0]
    c.Message.Content = c.Message.Content + "\n\n-- answered by callback"
    resp.Choices[0] = c
    return resp, nil
  })
```

---

## ToolCallbacks

- BeforeToolCallback：工具调用前触发
- AfterToolCallback：工具调用后触发

注意：Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

签名：

```go
// Before：可提前返回，并可通过指针修改参数
type BeforeToolCallback func(
  ctx context.Context,
  toolName string,
  toolDeclaration *tool.Declaration,
  jsonArgs *[]byte, // 指针：可修改，修改对调用方可见
) (any, error)

// After：可覆盖结果
type AfterToolCallback func(
  ctx context.Context,
  toolName string,
  toolDeclaration *tool.Declaration,
  jsonArgs []byte,
  result any,
  runErr error,
) (any, error)
```

参数修改（重要）：

- BeforeToolCallback 接收 `*[]byte`，回调内部可替换切片（如 `*jsonArgs = newBytes`）
- 修改后的参数将用于：
  - 实际工具执行
  - 可观测 Trace 与图事件（emitToolStartEvent/emitToolCompleteEvent）上报

提前返回：

- BeforeToolCallback 返回非空结果时，会跳过实际工具执行，直接使用该结果

示例：

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
    if jsonArgs != nil && toolName == "calculator" {
      origin := string(*jsonArgs)
      enriched := []byte(fmt.Sprintf(`{"original":%s,"ts":%d}`, origin, time.Now().Unix()))
      *jsonArgs = enriched
    }
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, args []byte, result any, runErr error) (any, error) {
    if runErr != nil {
      return nil, runErr
    }
    if s, ok := result.(string); ok {
      return s + "\n-- post processed by tool callback", nil
    }
    return result, nil
  })
```

可观测与事件：

- 修改后的参数会同步到：

  - `TraceToolCall` 可观测属性
  - 图事件 `emitToolStartEvent` 与 `emitToolCompleteEvent`

---

## AgentCallbacks

- BeforeAgentCallback：Agent 执行前触发
- AfterAgentCallback：Agent 执行后触发

签名：

```go
type BeforeAgentCallback func(ctx context.Context, inv *agent.Invocation) (*model.Response, error)
type AfterAgentCallback  func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error)
```

要点：

- Before 可返回自定义 `*model.Response` 以中止后续模型调用
- After 可返回替换响应
- Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

示例：

```go
agentCallbacks := agent.NewCallbacks().
  // Before：当用户消息包含 /abort 时，直接返回固定响应，跳过后续流程
  RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
    if inv != nil && strings.Contains(inv.GetUserMessageContent(), "/abort") {
      return &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "aborted by callback"}}}}, nil
    }
    return nil, nil
  }).
  // After：在成功响应末尾追加标注
  RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
    if runErr != nil {
      return nil, runErr
    }
    if inv == nil || inv.Response == nil || len(inv.Response.Choices) == 0 {
      return nil, nil
    }
    c := inv.Response.Choices[0]
    c.Message.Content = c.Message.Content + "\n\n-- handled by agent callback"
    inv.Response.Choices[0] = c
    return inv.Response, nil
  })
```

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
// 设置状态值.
func (inv *Invocation) SetState(key string, value any)

// 获取状态值，返回值和是否存在.
func (inv *Invocation) GetState(key string) (any, bool)

// 删除状态值.
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
// BeforeAgentCallback：记录开始时间.
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
  inv.SetState("agent:start_time", time.Now())
  return nil, nil
})

// AfterAgentCallback：计算执行时长.
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
  if startTimeVal, ok := inv.GetState("agent:start_time"); ok {
    startTime := startTimeVal.(time.Time)
    duration := time.Since(startTime)
    fmt.Printf("Agent execution took: %v\n", duration)
    inv.DeleteState("agent:start_time") // 清理状态.
  }
  return nil, nil
})
```

### 示例：Model 回调计时

Model 和 Tool 回调需要先从 context 中获取 Invocation：

```go
// BeforeModelCallback：记录开始时间.
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    inv.SetState("model:start_time", time.Now())
  }
  return nil, nil
})

// AfterModelCallback：计算执行时长.
modelCallbacks.RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    if startTimeVal, ok := inv.GetState("model:start_time"); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Model inference took: %v\n", duration)
      inv.DeleteState("model:start_time") // 清理状态.
    }
  }
  return nil, nil
})
```

### 示例：Tool 回调计时（支持并发工具调用）

当 LLM 在一次响应中返回多个工具调用（包括同一工具的多次调用）时，框架会并发执行这些工具。为了正确追踪每个工具调用的状态，需要使用 **tool call ID** 来区分：

```go
// BeforeToolCallback：记录工具开始时间.
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    // 获取 tool call ID 以支持并发调用.
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
      toolCallID = "default" // 降级方案.
    }

    // 使用 tool call ID 构建唯一键.
    key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
    inv.SetState(key, time.Now())
  }
  return nil, nil
})

// AfterToolCallback：计算工具执行时长.
toolCallbacks.RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    // 使用相同的逻辑获取 tool call ID.
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
      toolCallID = "default"
    }

    key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
    if startTimeVal, ok := inv.GetState(key); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Tool %s (call %s) took: %v\n", toolName, toolCallID, duration)
      inv.DeleteState(key) // 清理状态.
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
  RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    fmt.Printf("Global BeforeModel: %d messages.\n", len(req.Messages))
    return nil, nil
  }).
  RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, err error) (*model.Response, error) {
    fmt.Println("Global AfterModel: completed.")
    return nil, nil
  })

_ = tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
    fmt.Printf("Global BeforeTool: %s.\n", toolName)
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
    fmt.Printf("Global AfterTool: %s done.\n", toolName)
    return nil, nil
  })

_ = agent.NewCallbacks().
  RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
    fmt.Printf("Global BeforeAgent: %s.\n", inv.AgentName)
    return nil, nil
  }).
  RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
    fmt.Println("Global AfterAgent: completed.")
    return nil, nil
  })
```

---

## Mock 与参数修改示例

Mock 工具结果并中止后续工具调用：

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
  if toolName == "calculator" && jsonArgs != nil && strings.Contains(string(*jsonArgs), "42") {
    return calculatorResult{Operation: "custom", A: 42, B: 42, Result: 4242}, nil
  }
  return nil, nil
})
```

执行前修改参数（并在可观测/事件中体现）：

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
  if jsonArgs != nil && toolName == "calculator" {
    originalArgs := string(*jsonArgs)
    modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`, originalArgs, time.Now().Unix())
    *jsonArgs = []byte(modifiedArgs)
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
