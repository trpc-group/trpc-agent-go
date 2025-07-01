# Multi-turn Chat with Callbacks Example

This example demonstrates how to use the `Runner` orchestration component in a multi-turn chat system, with a focus on registering and utilizing **ModelCallbacks**, **ToolCallbacks**, and **AgentCallbacks**. These callbacks allow you to intercept, log, and customize key steps in LLM inference, tool invocation, and agent execution.

---

## Key Features

- **Multi-turn Conversation**: Maintains context across multiple user turns
- **Streaming Output**: Real-time streaming of model responses
- **Session Management**: Supports persistent chat sessions
- **Tool Integration**: Built-in calculator and time tools
- **Callback Mechanism**: Pluggable model, tool, and agent callbacks for extensibility and debugging

---

## Callback Mechanism Overview

### 1. ModelCallbacks

- **BeforeModelCallback**: Triggered before each model inference. Use for input interception, logging, or mocking responses.
- **AfterModelCallback**: Triggered on each streaming output chunk from the model (can be customized to print only on the first/last chunk). Use for output interception, content moderation, or logging.

**Example output:**

```
🔵 BeforeModelCallback: model=deepseek-chat, lastUserMsg="Hello"
🟣 AfterModelCallback: model=deepseek-chat has finished
```

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
🟢 BeforeAgentCallback: invocationID=..., userMsg="..."
🟡 AfterAgentCallback: invocationID=..., runErr=<nil>, userMsg="..."
```

---

## Declaring and Registering Callbacks

To use callbacks, you need to declare them and register your handler functions. Below are examples for each callback type:

### ModelCallbacks

```go
modelCallbacks := model.NewModelCallbacks()
modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
    // Your logic here
    return nil, nil
})
modelCallbacks.RegisterAfterModel(func(ctx context.Context, resp *model.Response, runErr error) (*model.Response, error) {
    // Your logic here
    return nil, nil
})
```

### ToolCallbacks

```go
toolCallbacks := tool.NewToolCallbacks()
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
    // Your logic here
    return nil, nil
})
toolCallbacks.RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
    // Your logic here
    return nil, nil
})
```

### AgentCallbacks

```go
agentCallbacks := agent.NewAgentCallbacks()
agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
    // Your logic here
    return nil, nil
})
agentCallbacks.RegisterAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
    // Your logic here
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

**Example: Mocking a tool result in BeforeToolCallback**

```go
toolCallbacks.RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, error) {
    if toolName == "calculator" && strings.Contains(string(jsonArgs), "42") {
        // Return a mock result and skip actual tool execution.
        return map[string]any{"result": 4242, "note": "mocked result"}, nil
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

2. Start the demo:

```bash
go run main.go
```

3. Follow the prompts to interact with the chat, trigger tool calls, and observe callback logs.

---

## Customizing Callbacks

- In `main.go`, look for `RegisterBeforeModel`, `RegisterAfterModel`, `RegisterBeforeTool`, `RegisterAfterTool`, `RegisterBeforeAgent`, and `RegisterAfterAgent` to customize callback logic.
- Callback functions can return custom responses (for mocking or interception) or simply perform logging/monitoring.
- Typical use cases:
  - Logging and tracing
  - Input/output interception and modification
  - Content safety and moderation
  - Tool mocking or fallback

---

## Typical Scenarios

- **Debugging LLM Pipelines**: Observe every step of input/output in real time
- **A/B Testing**: Dynamically switch models or tool implementations
- **Safety & Compliance**: Moderate model outputs and tool results
- **Business Extensions**: Insert custom business logic as needed

---

## References

- [runner/README.md](../runner/README.md) (basic multi-turn chat and tool calling)
- This directory's `main.go` (full callback registration and usage)

---

For advanced customization or production integration, see the source code or contact the maintainers.
