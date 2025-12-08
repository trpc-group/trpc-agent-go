## Callbacks

> **Version Requirement**  
> The structured callback API (recommended) requires **trpc-agent-go >= 0.6.0**.

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

### Structured Model Callbacks (Recommended)

- BeforeModelCallbackStructured: Runs before a model inference with structured arguments.
- AfterModelCallbackStructured: Runs after the model finishes with structured arguments.

Arguments:

```go
type BeforeModelArgs struct {
    Request *model.Request  // The request about to be sent (can be modified)
}

type BeforeModelResult struct {
    Context        context.Context  // Optional context for subsequent operations
    CustomResponse *model.Response  // If non-nil, skips model call and returns this response
}

type AfterModelArgs struct {
    Request  *model.Request   // The original request sent to the model
    Response *model.Response  // The response from the model (may be nil)
    Error    error            // Any error that occurred during model call
}

type AfterModelResult struct {
    Context        context.Context  // Optional context for subsequent operations
    CustomResponse *model.Response  // If non-nil, replaces the original response
}
```

Signatures:

```go
type BeforeModelCallbackStructured func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error)
type AfterModelCallbackStructured  func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error)
```

Key points:

- Structured parameters provide better type safety and clearer intent.
- `BeforeModelResult.Context` can be used to pass context to subsequent operations.
- `AfterModelResult.Context` allows passing context between callbacks.
- Before can return a non-nil response to skip the model call.
- After receives the original request, useful for content restoration and post-processing.

### Callback Execution Control

By default, callback execution stops immediately when:

- A callback returns an error
- A callback returns a non-nil `CustomResponse` (for Before callbacks) or `CustomResult` (for Tool callbacks)

You can control this behavior using options when creating callbacks:

```go
// Continue executing remaining callbacks even if an error occurs
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnError(true),
)

// Continue executing remaining callbacks even if a CustomResponse is returned
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnResponse(true),
)

// Enable both options: continue on both error and CustomResponse
modelCallbacks := model.NewCallbacks(
    model.WithContinueOnError(true),
    model.WithContinueOnResponse(true),
)
```

**Execution Modes:**

1. **Default (both false)**: Stop on first error or CustomResponse
2. **Continue on Error**: Continue executing remaining callbacks even if one returns an error
3. **Continue on Response**: Continue executing remaining callbacks even if one returns a CustomResponse
4. **Continue on Both**: Continue executing all callbacks regardless of errors or CustomResponse

**Priority Rules:**

- If both an error and a CustomResponse occur, the error takes priority and will be returned (unless `continueOnError` is true)
- When `continueOnError` is true and an error occurs, execution continues but the first error is preserved and returned at the end
- When `continueOnResponse` is true and a CustomResponse is returned, execution continues but the last CustomResponse is used

Example:

```go
modelCallbacks := model.NewCallbacks().
  // Before: respond to a special prompt to skip the real model call.
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    if len(args.Request.Messages) > 0 && strings.Contains(args.Request.Messages[len(args.Request.Messages)-1].Content, "/ping") {
      return &model.BeforeModelResult{
        CustomResponse: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "pong"}}}},
      }, nil
    }
    return nil, nil
  }).
  // After: annotate successful responses, keep errors untouched.
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

**Usage**: After creating callbacks, pass them to the LLM Agent when creating it using the `llmagent.WithModelCallbacks()` option:

```go
// Create model callbacks
modelCallbacks := model.NewCallbacks().
  RegisterBeforeModel(...).
  RegisterAfterModel(...)

// Create LLM Agent and pass model callbacks
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithModelCallbacks(modelCallbacks),  // Pass model callbacks
)
```

For a complete example, see [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go).

### Legacy Model Callbacks (Deprecated)

> **⚠️ Deprecated**  
> Legacy callbacks are deprecated. Use structured callbacks for new code.

---

## ToolCallbacks

### Structured Tool Callbacks (Recommended)

- BeforeToolCallbackStructured: Runs before each tool invocation with structured arguments.
- AfterToolCallbackStructured: Runs after each tool invocation with structured arguments.

Arguments:

```go
type BeforeToolArgs struct {
    ToolName     string               // The name of the tool
    Declaration  *tool.Declaration    // Tool declaration metadata
    Arguments    []byte               // JSON arguments (can be modified)
}

type BeforeToolResult struct {
    Context       context.Context     // Optional context for subsequent operations
    CustomResult  any                 // If non-nil, skips tool execution and returns this result
    ModifiedArguments []byte          // Optional modified arguments for tool execution
}

type AfterToolArgs struct {
    ToolName     string               // The name of the tool
    Declaration  *tool.Declaration    // Tool declaration metadata
    Arguments    []byte               // Original JSON arguments
    Result       any                  // Result from tool execution (may be nil)
    Error        error                // Any error that occurred during tool execution
}

type AfterToolResult struct {
    Context       context.Context     // Optional context for subsequent operations
    CustomResult  any                 // If non-nil, replaces the original result
}
```

Signatures:

```go
type BeforeToolCallbackStructured func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error)
type AfterToolCallbackStructured  func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error)
```

Key points:

- Structured parameters provide better type safety and clearer intent.
- `BeforeToolResult.ModifiedArguments` allows modifying tool arguments.
- `BeforeToolResult.Context` and `AfterToolResult.Context` can pass context between operations.
- Arguments can be modified directly via `args.Arguments`.
- If BeforeToolCallbackStructured returns a non-nil custom result, the tool is skipped and that result is used directly.

### Callback Execution Control

By default, callback execution stops immediately when:

- A callback returns an error
- A callback returns a non-nil `CustomResult`

You can control this behavior using options when creating callbacks:

```go
// Continue executing remaining callbacks even if an error occurs
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnError(true),
)

// Continue executing remaining callbacks even if a CustomResult is returned
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnResponse(true),
)

// Enable both options: continue on both error and CustomResult
toolCallbacks := tool.NewCallbacks(
    tool.WithContinueOnError(true),
    tool.WithContinueOnResponse(true),
)
```

**Execution Modes:**

1. **Default (both false)**: Stop on first error or CustomResult
2. **Continue on Error**: Continue executing remaining callbacks even if one returns an error
3. **Continue on Response**: Continue executing remaining callbacks even if one returns a CustomResult
4. **Continue on Both**: Continue executing all callbacks regardless of errors or CustomResult

**Priority Rules:**

- If both an error and a CustomResult occur, the error takes priority and will be returned (unless `continueOnError` is true)
- When `continueOnError` is true and an error occurs, execution continues but the first error is preserved and returned at the end
- When `continueOnResponse` is true and a CustomResult is returned, execution continues but the last CustomResult is used

Example:

```go
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.Arguments != nil && args.ToolName == "calculator" {
      // Enrich arguments.
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

**Usage**: After creating callbacks, pass them to the LLM Agent when creating it using the `llmagent.WithToolCallbacks()` option:

```go
// Create tool callbacks
toolCallbacks := tool.NewCallbacks().
  RegisterBeforeTool(...).
  RegisterAfterTool(...)

// Create LLM Agent and pass tool callbacks
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithTools(tools),
  llmagent.WithToolCallbacks(toolCallbacks),  // Pass tool callbacks
)
```

For a complete example, see [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go).

Telemetry and events:

- Modified arguments are propagated to:
  - `TraceToolCall` telemetry attributes.
  - Graph events emitted by `emitToolStartEvent` and `emitToolCompleteEvent`.

### Legacy Tool Callbacks (Deprecated)

> **⚠️ Deprecated**  
> Legacy callbacks are deprecated. Use structured callbacks for new code.

---

## AgentCallbacks

### Structured Agent Callbacks (Recommended)

- BeforeAgentCallbackStructured: Runs before agent execution with structured arguments.
- AfterAgentCallbackStructured: Runs after agent execution with structured arguments.

Arguments:

```go
type BeforeAgentArgs struct {
    Invocation *agent.Invocation  // The invocation context
}

type BeforeAgentResult struct {
    Context        context.Context  // Optional context for subsequent operations
    CustomResponse *model.Response  // If non-nil, skips agent execution and returns this response
}

type AfterAgentArgs struct {
    Invocation        *agent.Invocation  // The invocation context
    FullResponseEvent *event.Event       // The final response event from agent execution (may be nil)
    Error             error              // Any error that occurred during agent execution (may be nil)
}

type AfterAgentResult struct {
    Context        context.Context  // Optional context for subsequent operations
    CustomResponse *model.Response  // If non-nil, replaces the original response
}
```

Signatures:

```go
type BeforeAgentCallbackStructured func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error)
type AfterAgentCallbackStructured  func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error)
```

Key points:

- Structured parameters provide better type safety and clearer intent.
- `BeforeAgentResult.Context` and `AfterAgentResult.Context` can pass context between operations.
- Access to full invocation context allows for rich per-invocation logic.
- Before can short-circuit with a custom model.Response.
- After can return a replacement response.
- `AfterAgentArgs.FullResponseEvent` provides access to the final response event from agent execution, useful for logging, monitoring, post-processing, etc.

### Callback Execution Control

By default, callback execution stops immediately when:

- A callback returns an error
- A callback returns a non-nil `CustomResponse`

You can control this behavior using options when creating callbacks:

```go
// Continue executing remaining callbacks even if an error occurs
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnError(true),
)

// Continue executing remaining callbacks even if a CustomResponse is returned
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnResponse(true),
)

// Enable both options: continue on both error and CustomResponse
agentCallbacks := agent.NewCallbacks(
    agent.WithContinueOnError(true),
    agent.WithContinueOnResponse(true),
)
```

**Execution Modes:**

1. **Default (both false)**: Stop on first error or CustomResponse
2. **Continue on Error**: Continue executing remaining callbacks even if one returns an error
3. **Continue on Response**: Continue executing remaining callbacks even if one returns a CustomResponse
4. **Continue on Both**: Continue executing all callbacks regardless of errors or CustomResponse

**Priority Rules:**

- If both an error and a CustomResponse occur, the error takes priority and will be returned (unless `continueOnError` is true)
- When `continueOnError` is true and an error occurs, execution continues but the first error is preserved and returned at the end
- When `continueOnResponse` is true and a CustomResponse is returned, execution continues but the last CustomResponse is used

Example:

```go
agentCallbacks := agent.NewCallbacks().
  // Before: if the user message contains /abort, return a fixed response and skip the rest.
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    if args.Invocation != nil && strings.Contains(args.Invocation.GetUserMessageContent(), "/abort") {
      return &agent.BeforeAgentResult{
        CustomResponse: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "aborted by callback"}}}},
      }, nil
    }
    return nil, nil
  }).
  // After: append a footer to successful responses, can access FullResponseEvent for final response event.
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
      return nil, args.Error
    }
    // Can access the final response event from agent execution via FullResponseEvent.
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

**Usage**: After creating callbacks, pass them to the LLM Agent when creating it using the `llmagent.WithAgentCallbacks()` option:

```go
// Create agent callbacks
agentCallbacks := agent.NewCallbacks().
  RegisterBeforeAgent(...).
  RegisterAfterAgent(...)

// Create LLM Agent and pass agent callbacks
llmAgent := llmagent.New(
  "chat-assistant",
  llmagent.WithModel(modelInstance),
  llmagent.WithAgentCallbacks(agentCallbacks),  // Pass agent callbacks
)
```

### Stop agent via callbacks {#stop-agent-via-callbacks}

Use `agent.NewStopError` in callbacks when you need to halt execution and emit `stop_agent_error` to the runner stream.
This is useful for quota checks, guard rails, or manual aborts.

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

Notes:

- The flow turns the `StopError` into a `stop_agent_error` event and stops the loop. Downstream consumers can detect
  `event.Error.Type == agent.ErrorTypeStopAgentError`.
- Pair with context cancellation when you need a hard cutoff that also stops in-flight model calls or tool executions;
  see the runner docs for context cancellation patterns.

For a complete example, see [`examples/callbacks/main.go`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/callbacks/main.go).

### Legacy Agent Callbacks (Deprecated)

> **⚠️ Deprecated**  
> Legacy callbacks are deprecated. Use structured callbacks for new code.

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
agentCallbacks := agent.NewCallbacks().
  // BeforeAgentCallback: Record start time.
  RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    args.Invocation.SetState("agent:start_time", time.Now())
    return nil, nil
  }).
  // AfterAgentCallback: Calculate execution duration.
  RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if startTimeVal, ok := args.Invocation.GetState("agent:start_time"); ok {
      startTime := startTimeVal.(time.Time)
      duration := time.Since(startTime)
      fmt.Printf("Agent execution took: %v\n", duration)
      args.Invocation.DeleteState("agent:start_time") // Clean up state.
    }
    return nil, nil
  })
```

### Example: Model Callback Timing

Model and Tool callbacks need to retrieve the Invocation from context first:

```go
modelCallbacks := model.NewCallbacks().
  // BeforeModelCallback: Record start time.
  RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      inv.SetState("model:start_time", time.Now())
    }
    return nil, nil
  }).
  // AfterModelCallback: Calculate execution duration.
  RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
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
toolCallbacks := tool.NewCallbacks().
  // BeforeToolCallback: Record tool start time.
  RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      // Get tool call ID for concurrent call support.
      toolCallID, ok := tool.ToolCallIDFromContext(ctx)
      if !ok || toolCallID == "" {
        toolCallID = "default" // Fallback for compatibility.
      }

      // Use tool call ID to build unique key.
      key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
      inv.SetState(key, time.Now())
    }
    return nil, nil
  }).
  // AfterToolCallback: Calculate tool execution duration.
  RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
      // Get tool call ID for concurrent call support.
      toolCallID, ok := tool.ToolCallIDFromContext(ctx)
      if !ok || toolCallID == "" {
        toolCallID = "default" // Fallback for compatibility.
      }

      key := fmt.Sprintf("tool:%s:%s:start_time", args.ToolName, toolCallID)
      if startTimeVal, ok := inv.GetState(key); ok {
        startTime := startTimeVal.(time.Time)
        duration := time.Since(startTime)
        fmt.Printf("Tool %s (call %s) took: %v\n", args.ToolName, toolCallID, duration)
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

## Mocking and Argument Mutation Examples

Mock a tool result and short-circuit execution:

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

Modify arguments prior to execution (and telemetry/event reporting):

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
