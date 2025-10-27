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

## 在 Before 和 After 回调之间共享数据

回调系统提供了 **callback message** 机制，用于在 Before 和 After 回调之间共享可变数据。由于 Go 中的 `context.Context` 是不可变的，框架会自动向 context 中注入一个 message 对象，你可以使用它来存储和检索数据。

### 为什么需要 Callback Message？

如果没有 callback message，你需要维护外部状态（例如实例变量）来在回调之间共享数据：

```go
// 没有 callback message（不推荐）
type example struct {
    startTimes map[string]time.Time
}

func (e *example) beforeCallback(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
    e.startTimes[inv.InvocationID] = time.Now()
    return nil, nil
}

func (e *example) afterCallback(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
    if startTime, ok := e.startTimes[inv.InvocationID]; ok {
        duration := time.Since(startTime)
        fmt.Printf("Duration: %v\n", duration)
        delete(e.startTimes, inv.InvocationID)
    }
    return nil, nil
}
```

使用 callback message，数据自然地限定在每次回调调用的作用域内：

```go
// 使用 callback message（推荐）
func beforeCallback(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
    msg := agent.CallbackMessage(ctx)
    msg.Set("start_time", time.Now())
    return nil, nil
}

func afterCallback(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
    msg := agent.CallbackMessage(ctx)
    if startTimeVal, ok := msg.Get("start_time"); ok {
        if startTime, ok := startTimeVal.(time.Time); ok {
            duration := time.Since(startTime)
            fmt.Printf("Duration: %v\n", duration)
        }
    }
    return nil, nil
}
```

### Message API

`callback.Message` 接口提供四个方法：

```go
type Message interface {
    // Set 使用给定的 key 存储值
    Set(key string, value any)

    // Get 通过 key 检索值
    // 如果找到则返回 (value, true)，否则返回 (nil, false)
    Get(key string) (any, bool)

    // Delete 通过 key 删除值
    Delete(key string)

    // Clear 删除所有存储的值
    Clear()
}
```

### 访问 Callback Message

每种回调类型都有自己的访问器：

- **Agent 回调**：`agent.CallbackMessage(ctx)`
- **Model 回调**：`model.CallbackMessage(ctx)`
- **Tool 回调**：`tool.CallbackMessage(ctx)`

如果在 context 中找不到 message，所有访问器都会返回 `nil`。

### 完整示例

以下是一个完整示例，展示如何使用 callback message 测量执行时间：

```go
// Agent 回调
agentCallbacks := agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
        msg := agent.CallbackMessage(ctx)
        if msg != nil {
            msg.Set("start_time", time.Now())
            msg.Set("invocation_id", inv.InvocationID)
        }
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
        msg := agent.CallbackMessage(ctx)
        if msg == nil {
            return nil, nil
        }

        startTimeVal, ok := msg.Get("start_time")
        if !ok {
            return nil, nil
        }

        startTime, ok := startTimeVal.(time.Time)
        if !ok {
            return nil, nil
        }

        duration := time.Since(startTime)
        fmt.Printf("⏱️  Agent 执行耗时 %v\n", duration)

        return nil, nil
    })

// Model 回调
modelCallbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
        msg := model.CallbackMessage(ctx)
        if msg != nil {
            msg.Set("start_time", time.Now())
        }
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
        msg := model.CallbackMessage(ctx)
        if msg == nil {
            return nil, nil
        }

        if startTimeVal, ok := msg.Get("start_time"); ok {
            if startTime, ok := startTimeVal.(time.Time); ok {
                duration := time.Since(startTime)
                fmt.Printf("⏱️  模型推理耗时 %v\n", duration)
            }
        }

        return nil, nil
    })

// Tool 回调
toolCallbacks := tool.NewCallbacks().
    RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
        msg := tool.CallbackMessage(ctx)
        if msg != nil {
            msg.Set("start_time", time.Now())
            msg.Set("tool_name", toolName)
        }
        return nil, nil
    }).
    RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
        msg := tool.CallbackMessage(ctx)
        if msg == nil {
            return nil, nil
        }

        if startTimeVal, ok := msg.Get("start_time"); ok {
            if startTime, ok := startTimeVal.(time.Time); ok {
                duration := time.Since(startTime)
                fmt.Printf("⏱️  工具 %s 执行耗时 %v\n", toolName, duration)
            }
        }

        return nil, nil
    })
```

### 最佳实践

1. **始终检查 nil**：如果回调配置不正确，message 可能不存在。

2. **使用类型断言**：`Get` 方法返回 `any`，因此需要进行类型断言。

3. **使用有意义的 key**：使用描述性的 key 以避免冲突。

4. **需要时清理**：使用 `Delete` 或 `Clear` 在不再需要时删除数据。

### 线程安全说明

callback message 实现**不是线程安全的**。对于典型的回调场景，Before 和 After 回调在同一个 goroutine 中顺序执行，这不是问题。如果需要从多个 goroutine 访问 message，请添加自己的同步机制。

### Timer 示例

参见 [timer 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/timer)，这是一个完整的工作示例，使用 callback message 测量执行时间并上报到 OpenTelemetry。

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
