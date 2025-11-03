## Callbacks

This page describes the callback system used across the project to intercept,
observe, and customize model inference, tool invocation, and agent execution.

> **Note**: The legacy callback API is deprecated since trpc-agent-go v0.5.0. This documentation describes
> the new Structured Callbacks API which provides better type safety and extensibility.

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
type BeforeModelCallbackStructured func(
  ctx context.Context,
  args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error)

type AfterModelCallbackStructured func(
  ctx context.Context,
  args *model.AfterModelArgs,
) (*model.AfterModelResult, error)
```

Key points:

- Before can return a custom response via `BeforeModelResult.CustomResponse` to
  skip the model call.
- After receives the original request via `AfterModelArgs.Request`, useful for
  content restoration and post-processing.
- After can return a custom response via `AfterModelResult.CustomResponse` to
  replace the original response.

Example:

```go
modelCallbacks := model.NewCallbacksStructured().
  // Before: respond to a special prompt to skip the real model call.
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
  })
  // After: annotate successful responses, keep errors untouched.
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

- BeforeToolCallback: Runs before each tool invocation.
- AfterToolCallback: Runs after each tool invocation.

Signatures:

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

Argument mutation (important):

- BeforeToolCallback can modify arguments via
  `BeforeToolResult.ModifiedArguments`.
- The mutated arguments will be used for:
  - The actual tool execution.
  - Telemetry traces and graph events (emitToolStartEvent/emitToolCompleteEvent).

Short-circuiting:

- If BeforeToolCallback returns a non-nil `BeforeToolResult.CustomResult`, the
  tool is skipped and that result is used directly.

Example:

```go
toolCallbacks := tool.NewCallbacksStructured().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (
    *tool.BeforeToolResult, error,
  ) {
    if args.Arguments != nil && args.ToolName == "calculator" {
      // Enrich arguments.
      original := string(args.Arguments)
      enriched := []byte(fmt.Sprintf(`{"original":%s,"ts":%d}`,
        original, time.Now().Unix()))
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
type BeforeAgentCallbackStructured func(
  ctx context.Context,
  args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error)

type AfterAgentCallbackStructured func(
  ctx context.Context,
  args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error)
```

Key points:

- Before can short-circuit with a custom response via
  `BeforeAgentResult.CustomResponse`.
- After can return a replacement response via
  `AfterAgentResult.CustomResponse`.

Example:

```go
agentCallbacks := agent.NewCallbacksStructured().
  // Before: if the user message contains /abort, return a fixed response
  // and skip the rest.
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
  // After: append a footer to successful responses.
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

## Invocation State: Sharing Data Between Callbacks

`Invocation` provides a general-purpose `State` mechanism for storing invocation-scoped data. It can be used not only for sharing data between Before and After callbacks, but also for middleware, custom logic, and any invocation-level state management.

### Core Methods

```go
// Set a state value.
func (inv *Invocation) SetState(key string, value any)

// Get a state value, returns value and existence flag.
func (inv *Invocation) GetState(key string) (any, bool)

// Delete a state value.
func (inv *Invocation) DeleteState(key string)
```

### Features

- **Invocation-scoped**: State is automatically scoped to a single invocation
- **Thread-safe**: Built-in RWMutex protection for concurrent access
- **Lazy initialization**: Memory allocated only on first use
- **Clean lifecycle**: Explicit deletion prevents memory leaks
- **General-purpose**: Not limited to callbacks, can be used for any invocation-level state

### Naming Convention

To avoid key conflicts between different use cases, use prefixes:

- Agent callbacks: `"agent:xxx"` (e.g., `"agent:start_time"`)
- Model callbacks: `"model:xxx"` (e.g., `"model:start_time"`)
- Tool callbacks: `"tool:<toolName>:<toolCallID>:xxx"` (e.g., `"tool:calculator:call_abc123:start_time"`)
  - Note: Tool callbacks should include tool call ID to support concurrent calls
- Middleware: `"middleware:xxx"` (e.g., `"middleware:request_id"`)
- Custom logic: `"custom:xxx"` (e.g., `"custom:user_context"`)

### Example: Agent Callback Timing

```go
// BeforeAgentCallback: Record start time.
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
  inv.SetState("agent:start_time", time.Now())
  return nil, nil
})

// AfterAgentCallback: Calculate execution duration.
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
  if startTimeVal, ok := inv.GetState("agent:start_time"); ok {
    startTime := startTimeVal.(time.Time)
    duration := time.Since(startTime)
    fmt.Printf("Agent execution took: %v\n", duration)
    inv.DeleteState("agent:start_time") // Clean up state.
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
    inv.SetState("model:start_time", time.Now())
  }
  return nil, nil
})

// AfterModelCallback: Calculate execution duration.
modelCallbacks.RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    if startTimeVal, ok := inv.GetState("model:start_time"); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Model inference took: %v\n", duration)
      inv.DeleteState("model:start_time") // Clean up state.
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
    // Get tool call ID for concurrent call support.
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
      toolCallID = "default" // Fallback for compatibility.
    }

    // Use tool call ID to build unique key.
    key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
    inv.SetState(key, time.Now())
  }
  return nil, nil
})

// AfterToolCallback: Calculate tool execution duration.
toolCallbacks.RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
  if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    // Get tool call ID for concurrent call support.
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
      toolCallID = "default" // Fallback for compatibility.
    }

    key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
    if startTimeVal, ok := inv.GetState(key); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Tool %s (call %s) took: %v\n", toolName, toolCallID, duration)
      inv.DeleteState(key) // Clean up state.
    }
  }
  return nil, nil
})
```

**Key Points**:

1. **Get tool call ID**: Use `tool.ToolCallIDFromContext(ctx)` to retrieve the unique ID for each tool call from context
2. **Key format**: `"tool:<toolName>:<toolCallID>:<key>"` ensures state isolation for concurrent calls
3. **Fallback handling**: If tool call ID is not available (older versions or special scenarios), use `"default"` as fallback
4. **Consistency**: Before and After callbacks must use the same logic to retrieve tool call ID

This ensures that when the LLM calls `calculator` multiple times concurrently (e.g., `calculator(1,2)` and `calculator(3,4)`), each call has its own independent timing data.

### Complete Example

For a complete timing example with OpenTelemetry integration, see:
[examples/callbacks/timer](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/timer)

For an authentication and authorization example using Invocation State for permission checks and audit logging, see:
[examples/callbacks/auth](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/auth)

---

## Global Callbacks and Chain Registration

You can define reusable global callbacks using chain registration.

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

## Mocking and Argument Mutation Examples

Mock a tool result and short-circuit execution:

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

Modify arguments prior to execution (and telemetry/event reporting):

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
