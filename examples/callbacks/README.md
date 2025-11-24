# Multi-turn Chat with Callbacks Example

This example demonstrates how to use the `Runner` orchestration component in a multi-turn chat system, with a focus on registering and utilizing **ModelCallbacks**, **ToolCallbacks**, and **AgentCallbacks**. These callbacks allow you to intercept, log, and customize key steps in LLM inference, tool invocation, and agent execution.

---

## Key Features

- **Multi-turn Conversation**: Maintains context across multiple user turns
- **Streaming Output**: Real-time streaming of model responses
- **Session Management**: Uses in-memory session service (no external dependency)
- **Tool Integration**: Built-in calculator and time tools
- **Callback Mechanism**: Pluggable model, tool, and agent callbacks for extensibility and debugging
- **Command Line Interface**: Configurable model and streaming flags
- **Invocation access via context**: Callbacks can access `Invocation` using `agent.InvocationFromContext(ctx)`

---

## Project Layout

- `main.go`: App entry, wiring model, tools, agent, runner (in-memory session), and CLI flags
- `callbacks.go`: All callback registrations and helper logic
- `tools.go`: Tool implementations and their arguments/results

---

## Callback Mechanism Overview

### 1. ModelCallbacks

- **BeforeModelCallback**: Triggered before each model inference. Use for input interception, logging, or mocking responses.
- **AfterModelCallback**: Triggered on each streaming output chunk from the model (you can choose to log only the first/last chunk). Use for output interception, content moderation, or logging. The callback receives the original request for scenarios like content restoration and processing.

**Example output:**

```
🔵 BeforeModelCallback: model=deepseek-chat, lastUserMsg="Hello"
🔵 BeforeModelCallback: ✅ Invocation present in ctx (agent=..., id=...)
🟣 AfterModelCallback: model=deepseek-chat has finished
🟣 AfterModelCallback: detected 'original request' in user message: "show me original request"
🟣 AfterModelCallback: this demonstrates access to the original request in after callback.
```

**Key Feature**: The `AfterModelCallback` receives the original request as a parameter, enabling scenarios like:

- **Content Processing**: Access original input for post-processing workflows
- **Content Restoration**: Restore original formatting after model processing
- **Request/Response Correlation**: Compare original input with processed output

### 2. ToolCallbacks

- **BeforeToolCallback**: Triggered before each tool invocation. Use for argument validation, mocking tool results, logging, or **modifying tool arguments**. The `jsonArgs` parameter is a pointer, allowing callbacks to modify arguments that will be passed to the tool.
- **AfterToolCallback**: Triggered after each tool invocation. Use for result post-processing, formatting, or logging.

**Example output:**

```
🟠 BeforeToolCallback: tool=calculator, args={"operation":"add","a":1,"b":2}
🟠 BeforeToolCallback: ✅ Invocation present in ctx (agent=..., id=...)
🟠 BeforeToolCallback: Modified args for calculator: {"original":{"operation":"add","a":1,"b":2},"timestamp":"1703123456"}
🟤 AfterToolCallback: tool=calculator, args={...}, result=..., err=<nil>
```

**Key Feature**: The `BeforeToolCallback` receives `jsonArgs` as a pointer (`*[]byte`), enabling scenarios like:

- **Argument Modification**: Modify tool arguments before execution
- **Argument Validation**: Validate and sanitize inputs
- **Argument Enrichment**: Add metadata, timestamps, or context to arguments
- **Argument Transformation**: Convert or reformat arguments for specific tools

### 3. AgentCallbacks

- **BeforeAgentCallback**: Triggered before each agent execution. Use for input logging, permission checks, etc.
- **AfterAgentCallback**: Triggered after each agent execution. Use for output logging, error handling, etc.

**Example output:**

```
🟢 BeforeAgentCallback: agent=chat-assistant, invocationID=..., userMsg="..."
🟡 AfterAgentCallback: agent=chat-assistant, invocationID=..., runErr=<nil>, userMsg="..."
```

---

## Declaring and Registering Callbacks

The framework supports both traditional and chain registration patterns. Chain registration is recommended for cleaner code. See `callbacks.go` for complete, runnable examples.

### ModelCallbacks

```go
// Traditional registration
modelCallbacks := model.NewCallbacks()
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    // Your logic here - access request via args.Request
    return nil, nil
})
modelCallbacks.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    // Your logic here - access request via args.Request, response via args.Response
    return nil, nil
})

// Chain registration (recommended)
modelCallbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // Your logic here - now with access to original request
        return nil, nil
    })
```

### ToolCallbacks

```go
// Traditional registration
toolCallbacks := tool.NewCallbacks()
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    // Your logic here - access toolName via args.ToolName, arguments via args.Arguments
    return nil, nil
})
toolCallbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    // Your logic here - access toolName via args.ToolName, result via args.Result
    return nil, nil
})

// Chain registration (recommended)
toolCallbacks := tool.NewCallbacks().
    RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
        // Your logic here
        return nil, nil
    })
```

### AgentCallbacks

```go
// Traditional registration
agentCallbacks := agent.NewCallbacks()
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    // Your logic here - access invocation via args.Invocation
    return nil, nil
})
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    // Your logic here - access invocation via args.Invocation, error via args.Error
    return nil, nil
})

// Chain registration (recommended)
agentCallbacks := agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
        // Your logic here
        return nil, nil
    })
```

After declaring and registering your callbacks, pass them to the agent during construction (see `main.go`).

---

## Skipping Execution in Callbacks

You can short-circuit (skip) the default execution of a model, tool, or agent by returning a non-nil response/result from the corresponding callback. This is useful for mocking, early returns, blocking, or custom logic.

- **ModelCallbacks**: If `BeforeModelCallback` returns a non-nil `*model.Response`, the model will not be called and this response will be used directly.
- **ToolCallbacks**: If `BeforeToolCallback` returns a non-nil result, the tool will not be executed and this result will be used directly. Additionally, `BeforeToolCallback` can modify tool arguments via the `jsonArgs` pointer parameter.
- **AgentCallbacks**: If `BeforeAgentCallback` returns a non-nil `*model.Response`, the agent execution will be skipped and this response will be used.

**Example: Using original request in AfterModelCallback**

```go
modelCallbacks.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    // Access the original request for content restoration
    if args.Request != nil && len(args.Request.Messages) > 0 {
        originalText := args.Request.Messages[len(args.Request.Messages)-1].Content
        // Process response with original context
        if strings.Contains(originalText, "restore") {
            return &model.AfterModelResult{
                CustomResponse: restoreFormatting(args.Response, originalText),
            }, nil
        }
    }
    return nil, nil
})
```

**Example: Mocking a tool result in BeforeToolCallback**

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.ToolName == "calculator" && args.Arguments != nil && strings.Contains(string(args.Arguments), "42") {
        // Return a mock result and skip actual tool execution.
        return &tool.BeforeToolResult{
            CustomResult: calculatorResult{Operation: "custom", A: 42, B: 42, Result: 4242},
        }, nil
    }
    return nil, nil
})
```

**Example: Modifying tool arguments in BeforeToolCallback**

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    if args.Arguments != nil && args.ToolName == "calculator" {
        // Add timestamp to arguments for logging purposes
        originalArgs := string(args.Arguments)
        modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`, originalArgs, time.Now().Unix())
        args.Arguments = []byte(modifiedArgs)
        fmt.Printf("Modified args for %s: %s\n", args.ToolName, modifiedArgs)
    }
    return nil, nil
})
```

**Example: Blocking a model call in BeforeModelCallback**

```go
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
    if strings.Contains(args.Request.Messages[len(args.Request.Messages)-1].Content, "block me") {
        return &model.BeforeModelResult{
            CustomResponse: &model.Response{Choices: []model.Choice{{
                Message: model.Message{Role: model.RoleAssistant, Content: "This request was blocked by a callback."},
            }}},
        }, nil
    }
    return nil, nil
})
```

---

## Running the Example

1. Enter the directory and set your API key:

```bash
cd examples/callbacks
export OPENAI_API_KEY="your-api-key"
```

2. Start the demo with options:

```bash
# Basic usage (in-memory session service)
go run .

# With custom model
go run . -model gpt-4o-mini

# Disable streaming
go run . -streaming=false
```

3. Follow the prompts to interact with the chat, trigger tool calls, and observe callback logs.

**Try these test phrases:**

- `"show me original request"` - Demonstrates access to original request in after callback
- `"override me"` - Triggers response override in after callback
- `"custom model"` - Triggers custom response in before callback
- `"calculator 42 + 42"` - Triggers custom tool result in before tool callback
- `"/history"` - Show conversation history
- `"/new"` - Start a new session
- `"/exit"` - End the conversation

---

## Command Line Options

- `-model`: Model name to use (default: "deepseek-chat")
- `-streaming`: Enable streaming mode for responses (default: true)

---

## Customizing Callbacks

- In `callbacks.go`, see `RegisterBeforeModel`, `RegisterAfterModel`, `RegisterBeforeTool`, `RegisterAfterTool`, `RegisterBeforeAgent`, and `RegisterAfterAgent` to customize callback logic.
- Callback functions can return custom responses (for mocking or interception) or simply perform logging/monitoring.
- Typical use cases:
  - Logging and tracing
  - Input/output interception and modification
  - Content safety and moderation
  - Tool mocking or fallback
  - **Tool argument modification and enrichment**
  - **Original request access for content restoration**

---

## Typical Scenarios

- **Debugging LLM Pipelines**: Observe every step of input/output in real time
- **A/B Testing**: Dynamically switch models or tool implementations
- **Safety & Compliance**: Moderate model outputs and tool results
- **Business Extensions**: Insert custom business logic as needed
- **Content Processing**: Access original input for post-processing workflows
- **Argument Processing**: Modify, validate, or enrich tool arguments before execution

---

## Related Examples

- [Timer Example](./timer/) - Timing and telemetry with Invocation State and OpenTelemetry
- [Authentication and Authorization](./auth/) - User context and permission checks with Invocation State

---

## References

- `main.go` (wiring of model/agent/runner and in-memory session)
- `callbacks.go` (full callback registration and usage)
- `tools.go` (tool implementations)

For advanced customization or production integration, see the source code or contact the maintainers.
