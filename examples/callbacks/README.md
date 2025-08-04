# Multi-turn Chat with Callbacks Example

This example demonstrates how to use the `Runner` orchestration component in a multi-turn chat system, with a focus on registering and utilizing **ModelCallbacks**, **ToolCallbacks**, and **AgentCallbacks**. These callbacks allow you to intercept, log, and customize key steps in LLM inference, tool invocation, and agent execution.

---

## Key Features

- **Multi-turn Conversation**: Maintains context across multiple user turns
- **Streaming Output**: Real-time streaming of model responses
- **Session Management**: Supports persistent chat sessions (inmemory/redis)
- **Tool Integration**: Built-in calculator and time tools
- **Callback Mechanism**: Pluggable model, tool, and agent callbacks for extensibility and debugging
- **Command Line Interface**: Configurable via command-line flags
- **Performance Monitoring**: See [Timer Example](./timer/README.md) for comprehensive timing measurements with OpenTelemetry integration

---

## Callback Mechanism Overview

### 1. ModelCallbacks

- **BeforeModelCallback**: Triggered before each model inference. Use for input interception, logging, or mocking responses.
- **AfterModelCallback**: Triggered on each streaming output chunk from the model (can be customized to print only on the first/last chunk). Use for output interception, content moderation, or logging. The callback receives the original request for scenarios like content restoration and processing.

**Example output:**

```
🔵 BeforeModelCallback: model=deepseek-chat, lastUserMsg="Hello"
🟣 AfterModelCallback: model=deepseek-chat has finished
🟣 AfterModelCallback: detected 'original request' in user message: "show me original request"
🟣 AfterModelCallback: this demonstrates access to the original request in after callback.
```

**Key Feature**: The `AfterModelCallback` receives the original request as a parameter, enabling scenarios like:

- **Content Processing**: Access original input for post-processing workflows
- **Content Restoration**: Restore original formatting after model processing
- **Request/Response Correlation**: Compare original input with processed output

### 2. ToolCallbacks

- **BeforeToolCallback**: Triggered before each tool invocation. Use for argument validation, mocking tool results, or logging.
- **AfterToolCallback**: Triggered after each tool invocation. Use for result post-processing, formatting, or logging.

**Example output:**

```
🟠 BeforeToolCallback: tool=calculator, args={"operation":"add","a":1,"b":2}
🟤 AfterToolCallback: tool=calculator, args={...}, result=map[...], err=<nil>
```

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

To use callbacks, you need to declare them and register your handler functions. The framework supports both traditional and chain registration patterns. Chain registration is recommended for cleaner, more readable code.

### Chain Registration Benefits

- **Cleaner Code**: Single chain for multiple registrations
- **Global Configuration**: Easy to create reusable callback configurations
- **Better Readability**: More fluent and intuitive syntax
- **Reduced Boilerplate**: No need for multiple statements

Below are examples for each callback type:

### ModelCallbacks

```go
// Traditional registration
modelCallbacks := model.NewCallbacks()
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    // Your logic here
    return nil, nil
})
modelCallbacks.RegisterAfterModel(func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
    // Your logic here - now with access to original request
    return nil, nil
})

// Chain registration (recommended)
modelCallbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
        // Your logic here - now with access to original request
        return nil, nil
    })

// Global callback configuration
var globalModelCallbacks = model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
        fmt.Printf("Processing %d messages\n", len(req.Messages))
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
        if runErr != nil {
            fmt.Printf("Model error occurred\n")
        } else {
            fmt.Printf("Model processed successfully\n")
        }
        return nil, nil
    })
```

### ToolCallbacks

```go
// Traditional registration
toolCallbacks := tool.NewCallbacks()
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
    // Your logic here
    return nil, nil
})
toolCallbacks.RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
    // Your logic here
    return nil, nil
})

// Chain registration (recommended)
toolCallbacks := tool.NewCallbacks().
    RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
        // Your logic here
        return nil, nil
    })

// Global callback configuration
var globalToolCallbacks = tool.NewCallbacks().
    RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
        fmt.Printf("Executing tool: %s\n", toolName)
        return nil, nil
    }).
    RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
        if runErr != nil {
            fmt.Printf("Tool %s failed\n", toolName)
        } else {
            fmt.Printf("Tool %s completed\n", toolName)
        }
        return nil, nil
    })
```

### AgentCallbacks

```go
// Traditional registration
agentCallbacks := agent.NewCallbacks()
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
    // Your logic here
    return nil, nil
})
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
    // Your logic here
    return nil, nil
})

// Chain registration (recommended)
agentCallbacks := agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
        // Your logic here
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
        // Your logic here
        return nil, nil
    })

// Global callback configuration
var globalAgentCallbacks = agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
        fmt.Printf("Starting agent: %s\n", invocation.AgentName)
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
        if runErr != nil {
            fmt.Printf("Agent execution failed\n")
        } else {
            fmt.Printf("Agent execution completed\n")
        }
        return nil, nil
    })
```

After declaring and registering your callbacks, pass them to the agent or runner during construction:

```go
llmAgent := llmagent.New(
    ...,
    llmagent.WithModelCallbacks(modelCallbacks),
    llmagent.WithToolCallbacks(toolCallbacks),
    llmagent.WithAgentCallbacks(agentCallbacks),
)
```

### Skipping Execution in Callbacks

You can short-circuit (skip) the default execution of a model, tool, or agent by returning a non-nil response/result from the corresponding callback. This is useful for mocking, early returns, blocking, or custom logic.

- **ModelCallbacks**: If `BeforeModelCallback` returns a non-nil `*model.Response`, the model will not be called and this response will be used directly.
- **ToolCallbacks**: If `BeforeToolCallback` returns a non-nil result, the tool will not be executed and this result will be used directly.
- **AgentCallbacks**: If `BeforeAgentCallback` returns a non-nil `*model.Response`, the agent execution will be skipped and this response will be used.

**Example: Using original request in AfterModelCallback**

```go
modelCallbacks.RegisterAfterModel(func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
    // Access the original request for content restoration
    if req != nil && len(req.Messages) > 0 {
        originalText := req.Messages[len(req.Messages)-1].Content
        // Process response with original context
        if strings.Contains(originalText, "restore") {
            // Restore original formatting or structure
            return restoreFormatting(resp, originalText), nil
        }
    }
    return nil, nil
})
```

**Example: Mocking a tool result in BeforeToolCallback**

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
    if toolName == "calculator" && strings.Contains(string(jsonArgs), "42") {
        // Return a mock result and skip actual tool execution.
        return calculatorResult{
            Operation: "custom",
            A:         42,
            B:         42,
            Result:    4242,
        }, nil
    }
    return nil, nil
})
```

**Example: Blocking a model call in BeforeModelCallback**

```go
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    if strings.Contains(req.Messages[len(req.Messages)-1].Content, "block me") {
        // Return a custom response and skip model inference.
        return &model.Response{
            Choices: []model.Choice{{
                Message: model.Message{
                    Role:    model.RoleAssistant,
                    Content: "This request was blocked by a callback.",
                },
            }},
        }, nil
    }
    return nil, nil
})
```

This mechanism allows you to flexibly intercept, mock, or block any step in the agent/model/tool pipeline.

---

## Running the Example

1. Enter the directory and set your API key:

```bash
cd examples/callbacks
export OPENAI_API_KEY="your-api-key"
```

2. Start the demo with options:

```bash
# Basic usage
go run main.go

# With custom model
go run main.go -model gpt-4

# With Redis session storage
go run main.go -session redis -redis-addr localhost:6379

# Disable streaming
go run main.go -streaming=false
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
- `-redis-addr`: Redis address for session storage (default: "localhost:6379")
- `-session`: Session service type, "inmemory" or "redis" (default: "inmemory")
- `-streaming`: Enable streaming mode for responses (default: true)

---

## Customizing Callbacks

- In `main.go`, look for `RegisterBeforeModel`, `RegisterAfterModel`, `RegisterBeforeTool`, `RegisterAfterTool`, `RegisterBeforeAgent`, and `RegisterAfterAgent` to customize callback logic.
- Callback functions can return custom responses (for mocking or interception) or simply perform logging/monitoring.
- Typical use cases:
  - Logging and tracing
  - Input/output interception and modification
  - Content safety and moderation
  - Tool mocking or fallback
  - **Original request access for content restoration**

---

## Typical Scenarios

- **Debugging LLM Pipelines**: Observe every step of input/output in real time
- **A/B Testing**: Dynamically switch models or tool implementations
- **Safety & Compliance**: Moderate model outputs and tool results
- **Business Extensions**: Insert custom business logic as needed
- **Content Processing**: Access original input for post-processing workflows

---

## References

- [runner/README.md](../runner/README.md) (basic multi-turn chat and tool calling)
- This directory's `main.go` (full callback registration and usage)
- [Timer Example](./timer/README.md) (comprehensive timing measurements with OpenTelemetry integration for agent, model, and tool execution)

---

For advanced customization or production integration, see the source code or contact the maintainers.
