## 回调（Callbacks）

本文介绍项目中的回调系统，用于拦截、观测与定制模型推理、工具调用与 Agent 执行。

回调分为三类：

- ModelCallbacks（模型回调）
- ToolCallbacks（工具回调）
- AgentCallbacks（Agent 回调）

每类都有 Before 与 After 两种回调。Before 回调可以通过返回非空结果提前返回，跳过默认执行。

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

以上示例与 [`examples/callbacks`](../../../examples/callbacks/) 可运行示例保持一致。

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
