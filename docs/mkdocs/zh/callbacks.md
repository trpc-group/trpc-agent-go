## 回调（Callbacks）

本文介绍项目中的回调系统，用于拦截、观测与定制模型推理、工具调用、Agent 执行以及 AG-UI 事件翻译。

> **注意**：从 trpc-agent-go v0.5.0 版本开始，旧版回调 API 已废弃。本文档介绍新的 Structured Callbacks API，
> 提供更好的类型安全性和可扩展性。

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
type BeforeModelCallbackStructured func(
  ctx context.Context,
  args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error)

type AfterModelCallbackStructured func(> **注意**：旧版回调 API 已废弃。本文档介绍新的 Structured Callbacks API，
> 提供更好的类型安全性和可扩展性。

  args *model.AfterModelArgs,
) (*model.AfterModelResult, error)
```

要点：

- Before 可通过 `BeforeModelResult.CustomResponse` 返回自定义响应以跳过模型调用
- After 可通过 `AfterModelArgs.Request` 获取原始请求，便于内容还原与后处理
- After 可通过 `AfterModelResult.CustomResponse` 返回自定义响应以替换原始响应
- Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

示例：

```go
modelCallbacks := model.NewCallbacksStructured().
  // Before：对特定提示直接返回固定响应，跳过真实模型调用
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (
    *model.BeforeModelResult, error,
  ) {
    if len(args.Request.Messages) > 0 &&
      strings.Contains(args.Request.Messages[len(args.Request.Messages)-1].Content, "/ping") {
      return &model.BeforeModelResult{
        CustomResponse: &model.Response{
          Choices: []model.Choice{{
            Message: model.Message{
              Role:    model.RoleAssistant,
              Content: "pong",
            },
          }},
        },
      }, nil
    }
    return nil, nil
  }).
  // After：在成功时追加提示信息，或在出错时包装错误信息
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (
    *model.AfterModelResult, error,
  ) {
    if args.Error != nil || args.Response == nil ||
      len(args.Response.Choices) == 0 {
      return nil, nil
    }
    c := args.Response.Choices[0]
    c.Message.Content = c.Message.Content + "\n\n-- answered by callback"
    return &model.AfterModelResult{
      CustomResponse: &model.Response{
        Choices: []model.Choice{c},
      },
    }, nil
  })
```

---

## ToolCallbacks

- BeforeToolCallback：工具调用前触发
- AfterToolCallback：工具调用后触发

注意：Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

签名：

```go
type BeforeToolCallbackStructured func(
  ctx context.Context,
  args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error)

type AfterToolCallbackStructured func(
  ctx context.Context,
  args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error)
```

参数修改（重要）：

- BeforeToolCallback 可通过 `BeforeToolResult.ModifiedArguments` 修改参数
- 修改后的参数将用于：
  - 实际工具执行
  - 可观测 Trace 与图事件（emitToolStartEvent/emitToolCompleteEvent）上报

提前返回：

- BeforeToolCallback 返回非空 `BeforeToolResult.CustomResult` 时，会跳过实际工具执行，直接使用该结果

示例：

```go
toolCallbacks := tool.NewCallbacksStructured().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (
    *tool.BeforeToolResult, error,
  ) {
    if args.Arguments != nil && args.ToolName == "calculator" {
      origin := string(args.Arguments)
      enriched := []byte(fmt.Sprintf(`{"original":%s,"ts":%d}`,
        origin, time.Now().Unix()))
      return &tool.BeforeToolResult{
        ModifiedArguments: enriched,
      }, nil
    }
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (
    *tool.AfterToolResult, error,
  ) {
    if args.Error != nil {
      return nil, nil
    }
    if s, ok := args.Result.(string); ok {
      return &tool.AfterToolResult{
        CustomResult: s + "\n-- post processed by tool callback",
      }, nil
    }
    return nil, nil
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
type BeforeAgentCallbackStructured func(
  ctx context.Context,
  args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error)

type AfterAgentCallbackStructured func(
  ctx context.Context,
  args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error)
```

要点：

- Before 可通过 `BeforeAgentResult.CustomResponse` 返回自定义响应以中止后续模型调用
- After 可通过 `AfterAgentResult.CustomResponse` 返回替换响应
- Before/After 回调遵循全局短路规则，若要叠加修改请在单个回调内完成合并逻辑

示例：

```go
agentCallbacks := agent.NewCallbacksStructured().
  // Before：当用户消息包含 /abort 时，直接返回固定响应，跳过后续流程
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (
    *agent.BeforeAgentResult, error,
  ) {
    if args.Invocation != nil &&
      strings.Contains(args.Invocation.GetUserMessageContent(), "/abort") {
      return &agent.BeforeAgentResult{
        CustomResponse: &model.Response{
          Choices: []model.Choice{{
            Message: model.Message{
              Role:    model.RoleAssistant,
              Content: "aborted by callback",
            },
          }},
        },
      }, nil
    }
    return nil, nil
  }).
  // After：在成功响应末尾追加标注
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (
    *agent.AfterAgentResult, error,
  ) {
    if args.Error != nil {
      return nil, nil
    }
    inv := args.Invocation
    if inv == nil || inv.Response == nil || len(inv.Response.Choices) == 0 {
      return nil, nil
    }
    c := inv.Response.Choices[0]
    c.Message.Content = c.Message.Content + "\n\n-- handled by agent callback"
    return &agent.AfterAgentResult{
      CustomResponse: &model.Response{
        Choices: []model.Choice{c},
      },
    }, nil
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

## 全局回调与链式注册

可通过链式注册构建可复用的全局回调配置。

```go
_ = model.NewCallbacksStructured().
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (
    *model.BeforeModelResult, error,
  ) {
    fmt.Printf("Global BeforeModel: %d messages.\n",
      len(args.Request.Messages))
    return nil, nil
  }).
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (
    *model.AfterModelResult, error,
  ) {
    fmt.Println("Global AfterModel: completed.")
    return nil, nil
  })

_ = tool.NewCallbacksStructured().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (
    *tool.BeforeToolResult, error,
  ) {
    fmt.Printf("Global BeforeTool: %s.\n", args.ToolName)
    return nil, nil
  }).
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (
    *tool.AfterToolResult, error,
  ) {
    fmt.Printf("Global AfterTool: %s done.\n", args.ToolName)
    return nil, nil
  })

_ = agent.NewCallbacksStructured().
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (
    *agent.BeforeAgentResult, error,
  ) {
    fmt.Printf("Global BeforeAgent: %s.\n", args.Invocation.AgentName)
    return nil, nil
  }).
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (
    *agent.AfterAgentResult, error,
  ) {
    fmt.Println("Global AfterAgent: completed.")
    return nil, nil
  })
```

---

## Mock 与参数修改示例

Mock 工具结果并中止后续工具调用：

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (
  *tool.BeforeToolResult, error,
) {
  if args.ToolName == "calculator" && args.Arguments != nil &&
    strings.Contains(string(args.Arguments), "42") {
    return &tool.BeforeToolResult{
      CustomResult: calculatorResult{
        Operation: "custom",
        A:         42,
        B:         42,
        Result:    4242,
      },
    }, nil
  }
  return nil, nil
})
```

执行前修改参数（并在可观测/事件中体现）：

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (
  *tool.BeforeToolResult, error,
) {
  if args.Arguments != nil && args.ToolName == "calculator" {
    originalArgs := string(args.Arguments)
    modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`,
      originalArgs, time.Now().Unix())
    return &tool.BeforeToolResult{
      ModifiedArguments: []byte(modifiedArgs),
    }, nil
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
