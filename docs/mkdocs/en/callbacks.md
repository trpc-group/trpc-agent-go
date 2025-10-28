## Callbacks

This page describes the callback system used across the project to intercept,
observe, and customize model inference, tool invocation, and agent execution.

The callback system comes in three categories:

- ModelCallbacks
- ToolCallbacks
- AgentCallbacks

Each category provides a Before and an After callback. A Before callback can
short-circuit the default execution by returning a non-nil custom response.

---

## ModelCallbacks

- BeforeModelCallback: Runs before a model inference.
- AfterModelCallback: Runs after the model finishes (or per streaming phase).

Signatures:

```go
type BeforeModelCallback func(ctx context.Context, req *model.Request) (*model.Response, error)
type AfterModelCallback  func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error)
```

Key points:

- Before can return a non-nil response to skip the model call.
- After receives the original request, useful for content restoration and
  post-processing.

Example:

```go
modelCallbacks := model.NewCallbacks().
  // Before: respond to a special prompt to skip the real model call.
  RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    if len(req.Messages) > 0 && strings.Contains(req.Messages[len(req.Messages)-1].Content, "/ping") {
      return &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "pong"}}}}, nil
    }
    return nil, nil
  }).
  // After: annotate successful responses, keep errors untouched.
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

- BeforeToolCallback: Runs before each tool invocation.
- AfterToolCallback: Runs after each tool invocation.

Signatures:

```go
// Before: can short-circuit with a custom result and can mutate arguments via pointer.
type BeforeToolCallback func(
  ctx context.Context,
  toolName string,
  toolDeclaration *tool.Declaration,
  jsonArgs *[]byte, // pointer: mutations are visible to the caller
) (any, error)

// After: can override the result.
type AfterToolCallback func(
  ctx context.Context,
  toolName string,
  toolDeclaration *tool.Declaration,
  jsonArgs []byte,
  result any,
  runErr error,
) (any, error)
```

Argument mutation (important):

- jsonArgs is passed as a pointer (`*[]byte`) to BeforeToolCallback.
- The callback may replace the slice (e.g., `*jsonArgs = newBytes`).
- The mutated arguments will be used for:
  - The actual tool execution.
  - Telemetry traces and graph events (emitToolStartEvent/emitToolCompleteEvent).

Short-circuiting:

- If BeforeToolCallback returns a non-nil custom result, the tool is skipped
  and that result is used directly.

Example:

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
    if jsonArgs != nil && toolName == "calculator" {
      // Enrich arguments.
      original := string(*jsonArgs)
      enriched := []byte(fmt.Sprintf(`{"original":%s,"ts":%d}`, original, time.Now().Unix()))
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

Telemetry and events:

- Modified arguments are propagated to:
  - `TraceToolCall` telemetry attributes.
  - Graph events emitted by `emitToolStartEvent` and `emitToolCompleteEvent`.

---

## AgentCallbacks

- BeforeAgentCallback: Runs before agent execution.
- AfterAgentCallback: Runs after agent execution.

Signatures:

```go
type BeforeAgentCallback func(ctx context.Context, inv *agent.Invocation) (*model.Response, error)
type AfterAgentCallback  func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error)
```

Key points:

- Before can short-circuit with a custom model.Response.
- After can return a replacement response.

Example:

```go
agentCallbacks := agent.NewCallbacks().
  // Before: if the user message contains /abort, return a fixed response and skip the rest.
  RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
    if inv != nil && strings.Contains(inv.GetUserMessageContent(), "/abort") {
      return &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "aborted by callback"}}}}, nil
    }
    return nil, nil
  }).
  // After: append a footer to successful responses.
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

## Access Invocation in Callbacks

Callbacks can access the current agent invocation via context to correlate
events, add tracing, or implement per-invocation logic.

```go
if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
  fmt.Printf("invocation id=%s, agent=%s\n", inv.InvocationID, inv.AgentName)
}
```

This pattern is showcased in the examples where Before/After callbacks print
the presence of an invocation.

---

## Callback State: Sharing Data Between Callbacks

`Invocation` provides a `CallbackState` mechanism for sharing data between
Before and After callbacks, particularly useful for timing, tracing, and other
scenarios requiring state persistence across callback pairs.

### Core Methods

```go
// Set a state value.
func (inv *Invocation) SetCallbackState(key string, value any)

// Get a state value, returns value and existence flag.
func (inv *Invocation) GetCallbackState(key string) (any, bool)

// Delete a state value.
func (inv *Invocation) DeleteCallbackState(key string)
```

### Features

- **Invocation-scoped**: State is automatically scoped to a single invocation
- **Thread-safe**: Built-in RWMutex protection for concurrent access
- **Lazy initialization**: Memory allocated only on first use
- **Clean lifecycle**: Explicit deletion prevents memory leaks

### Naming Convention

To avoid key conflicts between different callback types, use prefixes:

- Agent callbacks: `"agent:xxx"` (e.g., `"agent:start_time"`)
- Model callbacks: `"model:xxx"` (e.g., `"model:start_time"`)
- Tool callbacks: `"tool:toolName:xxx"` (e.g., `"tool:calculator:start_time"`)

### Example: Agent Callback Timing

```go
// BeforeAgentCallback: Record start time.
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
  inv.SetCallbackState("agent:start_time", time.Now())
  return nil, nil
})

// AfterAgentCallback: Calculate execution duration.
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
  if startTimeVal, ok := inv.GetCallbackState("agent:start_time"); ok {
    startTime := startTimeVal.(time.Time)
    duration := time.Since(startTime)
    fmt.Printf("Agent execution took: %v\n", duration)
    inv.DeleteCallbackState("agent:start_time") // Clean up state.
  }
  return nil, nil
})
```

### Example: Model Callback Timing

Model and Tool callbacks need to retrieve the Invocation from context first:

```go
// BeforeModelCallback: Record start time.
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    inv.SetCallbackState("model:start_time", time.Now())
  }
  return nil, nil
})

// AfterModelCallback: Calculate execution duration.
modelCallbacks.RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    if startTimeVal, ok := inv.GetCallbackState("model:start_time"); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Model inference took: %v\n", duration)
      inv.DeleteCallbackState("model:start_time") // Clean up state.
    }
  }
  return nil, nil
})
```

### Example: Tool Callback Timing (Multi-tool Isolation)

```go
// BeforeToolCallback: Record tool start time.
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    key := "tool:" + toolName + ":start_time"
    inv.SetCallbackState(key, time.Now())
  }
  return nil, nil
})

// AfterToolCallback: Calculate tool execution duration.
toolCallbacks.RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    key := "tool:" + toolName + ":start_time"
    if startTimeVal, ok := inv.GetCallbackState(key); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Tool %s took: %v\n", toolName, duration)
      inv.DeleteCallbackState(key) // Clean up state.
    }
  }
  return nil, nil
})
```

### Complete Example

For a complete timing example with OpenTelemetry integration, see:
[examples/callbacks/timer](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/timer)

---

## Global Callbacks and Chain Registration

You can define reusable global callbacks using chain registration.

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
    // jsonArgs is a pointer; modifications are visible to the caller.
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

## Mocking and Argument Mutation Examples

Mock a tool result and short-circuit execution:

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs *[]byte) (any, error) {
  if toolName == "calculator" && jsonArgs != nil && strings.Contains(string(*jsonArgs), "42") {
    return calculatorResult{Operation: "custom", A: 42, B: 42, Result: 4242}, nil
  }
  return nil, nil
})
```

Modify arguments prior to execution (and telemetry/event reporting):

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

Both examples mirror the runnable demo under [examples/callbacks](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks).

---

## Running the Callbacks Example

```bash
cd examples/callbacks
export OPENAI_API_KEY="your-api-key"

# Basic
go run .

# Choose model
go run . -model gpt-4o-mini

# Disable streaming
go run . -streaming=false
```

Observe logs for Before/After callbacks, argument mutation messages, and tool
responses.
