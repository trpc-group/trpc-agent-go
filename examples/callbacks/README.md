# Comprehensive Callbacks Example

This example demonstrates the usage of all 6 types of callbacks available in the trpc-agent-go framework:

1. **Before Agent Callbacks** - Execute before agent runs
2. **After Agent Callbacks** - Execute after agent completes
3. **Before Model Callbacks** - Execute before model is called
4. **After Model Callbacks** - Execute after model responds
5. **Before Tool Callbacks** - Execute before tool execution
6. **After Tool Callbacks** - Execute after tool execution

## Overview

Callbacks provide a powerful mechanism to intercept, modify, or skip execution at various stages of the agent workflow. They enable:

- **Logging and Monitoring** - Track execution flow and performance
- **Permission Control** - Validate access before execution
- **Caching** - Store and retrieve results to improve performance
- **Error Handling** - Gracefully handle failures
- **Result Transformation** - Modify outputs to match requirements
- **Debugging** - Add detailed logging for troubleshooting

## Execution Flow

```
1. Before Agent Callbacks
2. Agent Execution
   ├── Before Model Callbacks
   ├── Model Call
   ├── After Model Callbacks
   ├── Before Tool Callbacks (when tools are called)
   ├── Tool Execution
   ├── After Tool Callbacks
   └── Repeat steps 2-6 as needed
3. After Agent Callbacks
```

## Callback Types

### 1. Agent Callbacks

#### Before Agent Callback

```go
type BeforeAgentCallback func(
    ctx context.Context,
    invocation *agent.Invocation
) (*model.Response, bool, error)
```

**Returns:**

- `customResponse`: If not nil, this response will be returned and agent execution will be skipped
- `skip`: If true, agent execution will be skipped
- `error`: If not nil, agent execution will be stopped with this error

#### After Agent Callback

```go
type AfterAgentCallback func(
    ctx context.Context,
    invocation *agent.Invocation,
    runErr error
) (*model.Response, bool, error)
```

**Returns:**

- `customResponse`: If not nil and override is true, this response will be used instead of the actual agent response
- `override`: If true, the customResponse will be used
- `error`: If not nil, this error will be returned

### 2. Model Callbacks

#### Before Model Callback

```go
type BeforeModelCallback func(
    ctx context.Context,
    request *model.Request
) (*model.Response, bool, error)
```

**Returns:**

- `customResponse`: If not nil, this response will be returned and model call will be skipped
- `skip`: If true, model call will be skipped
- `error`: If not nil, model call will be stopped with this error

#### After Model Callback

```go
type AfterModelCallback func(
    ctx context.Context,
    response *model.Response,
    modelErr error
) (*model.Response, bool, error)
```

**Returns:**

- `customResponse`: If not nil and override is true, this response will be used instead of the actual model response
- `override`: If true, the customResponse will be used
- `error`: If not nil, this error will be returned

### 3. Tool Callbacks

#### Before Tool Callback

```go
type BeforeToolCallback func(
    ctx context.Context,
    toolName string,
    toolDeclaration *tool.Declaration,
    jsonArgs []byte
) (any, bool, error)
```

**Returns:**

- `customResult`: If not nil, this result will be returned and tool execution will be skipped
- `skip`: If true, tool execution will be skipped
- `error`: If not nil, tool execution will be stopped with this error

#### After Tool Callback

```go
type AfterToolCallback func(
    ctx context.Context,
    toolName string,
    toolDeclaration *tool.Declaration,
    jsonArgs []byte,
    result any,
    runErr error
) (any, bool, error)
```

**Returns:**

- `customResult`: If not nil and override is true, this result will be used instead of the actual tool result
- `override`: If true, the customResult will be used
- `error`: If not nil, this error will be returned

## Usage Examples

### Basic Setup

```go
// Create callbacks
agentCallbacks := agent.NewAgentCallbacks()
modelCallbacks := model.NewModelCallbacks()
toolCallbacks := tool.NewToolCallbacks()

// Create agent with callbacks
llmAgent := llmagent.New("example-agent", llmagent.Options{
    Model:          llm,
    AgentCallbacks: agentCallbacks,
    ModelCallbacks: modelCallbacks,
    Tools:          []tool.Tool{calculatorTool, weatherTool},
})

// Create invocation with callbacks
invocation := &agent.Invocation{
    Agent:          llmAgent,
    AgentName:      "example-agent",
    InvocationID:   "example-invocation",
    Model:          llm,
    Message:        model.NewUserMessage("Your message here"),
    AgentCallbacks: agentCallbacks,
    ModelCallbacks: modelCallbacks,
    ToolCallbacks:  toolCallbacks,
}
```

### Agent Callbacks Example

```go
// Before Agent Callback
agentCallbacks.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
    fmt.Printf("Agent %s starting execution\n", invocation.AgentName)

    // Skip execution for certain conditions
    if invocation.Message.Content == "skip" {
        return &model.Response{
            Choices: []model.Choice{{
                Message: model.Message{
                    Role:    model.RoleAssistant,
                    Content: "Execution skipped by callback",
                },
            }},
        }, true, nil
    }

    return nil, false, nil
})

// After Agent Callback
agentCallbacks.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
    if runErr != nil {
        fmt.Printf("Agent execution failed: %v\n", runErr)
        return &model.Response{
            Choices: []model.Choice{{
                Message: model.Message{
                    Role:    model.RoleAssistant,
                    Content: "Error handled gracefully",
                },
            }},
        }, true, nil
    }

    fmt.Printf("Agent %s completed successfully\n", invocation.AgentName)
    return nil, false, nil
})
```

### Model Callbacks Example

```go
// Before Model Callback
modelCallbacks.AddBeforeModel(func(ctx context.Context, request *model.Request) (*model.Response, bool, error) {
    fmt.Printf("Model call with %d messages and %d tools\n",
        len(request.Messages), len(request.Tools))

    // Skip model call for empty requests
    if len(request.Messages) == 0 {
        return &model.Response{
            Choices: []model.Choice{{
                Message: model.Message{
                    Role:    model.RoleAssistant,
                    Content: "No messages to process",
                },
            }},
        }, true, nil
    }

    return nil, false, nil
})

// After Model Callback
modelCallbacks.AddAfterModel(func(ctx context.Context, response *model.Response, runErr error) (*model.Response, bool, error) {
    if runErr != nil {
        fmt.Printf("Model call failed: %v\n", runErr)
        return &model.Response{
            Choices: []model.Choice{{
                Message: model.Message{
                    Role:    model.RoleAssistant,
                    Content: "Model error handled gracefully",
                },
            }},
        }, true, nil
    }

    fmt.Printf("Model call successful with %d choices\n", len(response.Choices))
    return nil, false, nil
})
```

### Tool Callbacks Example

```go
// Before Tool Callback
toolCallbacks.AddBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
    fmt.Printf("Executing tool: %s with args: %s\n", toolName, string(jsonArgs))

    // Skip specific tools
    if toolName == "skip-tool" {
        return map[string]string{"skipped": "true"}, true, nil
    }

    // Return custom result for specific conditions
    if toolName == "calculator" {
        var args CalculatorInput
        if err := json.Unmarshal(jsonArgs, &args); err == nil && args.A == 0 && args.B == 0 {
            return CalculatorOutput{Result: 42}, false, nil
        }
    }

    return nil, false, nil
})

// After Tool Callback
toolCallbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
    if runErr != nil {
        fmt.Printf("Tool %s execution failed: %v\n", toolName, runErr)
        return map[string]string{"error": "handled"}, true, nil
    }

    fmt.Printf("Tool %s executed successfully: %v\n", toolName, result)

    // Override result for specific conditions
    if toolName == "calculator" {
        if calcResult, ok := result.(CalculatorOutput); ok {
            return map[string]string{
                "formatted_result": fmt.Sprintf("The answer is %d", calcResult.Result),
                "original_result":  fmt.Sprintf("%d", calcResult.Result),
            }, true, nil
        }
    }

    return nil, false, nil
})
```

## Common Use Cases

### 1. Logging and Monitoring

```go
// Add timing information
startTime := time.Now()
callbacks.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
    ctx = context.WithValue(ctx, "startTime", startTime)
    return nil, false, nil
})

callbacks.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
    if startTime, ok := ctx.Value("startTime").(time.Time); ok {
        duration := time.Since(startTime)
        fmt.Printf("Agent execution took: %v\n", duration)
    }
    return nil, false, nil
})
```

### 2. Permission Control

```go
callbacks.AddBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
    if !hasPermission(ctx, toolName) {
        return map[string]string{"error": "permission denied"}, true, nil
    }
    return nil, false, nil
})
```

### 3. Caching

```go
cache := make(map[string]any)

callbacks.AddBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
    cacheKey := fmt.Sprintf("%s:%s", toolName, string(jsonArgs))
    if cached, found := cache[cacheKey]; found {
        return cached, true, nil
    }
    return nil, false, nil
})

callbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
    if runErr == nil {
        cacheKey := fmt.Sprintf("%s:%s", toolName, string(jsonArgs))
        cache[cacheKey] = result
    }
    return nil, false, nil
})
```

### 4. Error Handling

```go
callbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
    if runErr != nil {
        // Log error and return graceful fallback
        log.Printf("Tool %s failed: %v", toolName, runErr)
        return map[string]string{
            "error": "Tool execution failed",
            "fallback": "Using default value",
        }, true, nil
    }
    return nil, false, nil
})
```

### 5. Result Transformation

```go
callbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
    if runErr != nil {
        return nil, false, runErr
    }

    // Add metadata to all results
    return map[string]interface{}{
        "data": result,
        "metadata": map[string]string{
            "source": "callback",
            "timestamp": time.Now().Format(time.RFC3339),
        },
    }, true, nil
})
```

## Best Practices

### 1. Error Handling

- Always handle errors gracefully in callbacks
- Use `tool.NewError()` for tool-specific errors
- Provide meaningful fallback responses

### 2. Performance

- Avoid expensive operations in callbacks
- Use async processing for logging and monitoring
- Implement caching to reduce redundant operations

### 3. Debugging

- Add detailed logging in development
- Use conditional logging based on environment
- Include context information in logs

### 4. Testing

- Write unit tests for callback logic
- Test error scenarios
- Verify callback execution order

### 5. Security

- Validate inputs in before callbacks
- Implement proper permission checks
- Sanitize outputs in after callbacks

## Running the Example

1. **Set up your environment:**

   ```bash
   export OPENAI_API_KEY="your-api-key-here"
   ```

2. **Run the example:**

   ```bash
   go run main.go
   ```

3. **Expected output:**

   ```
   🚀 Starting Comprehensive Callbacks Example
   ==========================================
   📝 User Message: Please calculate 5 + 3 and tell me the weather in Beijing

   🔄 Before Agent Callback:
      - Agent: example-agent
      - Invocation ID: example-invocation
      - Message: Please calculate 5 + 3 and tell me the weather in Beijing
      ✅ Proceeding with normal agent execution

   🔄 Before Model Callback:
      - Messages count: 1
      - Tools count: 2
      ✅ Proceeding with normal model call

   🔧 Tool calls detected:
      - Tool: calculator
      - Args: {"a":5,"b":3}

   🔄 Before Tool Callback:
      - Tool: calculator
      - Args: {"a":5,"b":3}
      ✅ Proceeding with normal tool execution

   🔄 After Tool Callback:
      - Tool: calculator
      ✅ Tool execution successful, result: {Result:8}
      🎯 Overriding calculator result

   ✅ Tool response: {"formatted_result":"The answer is 8","original_result":"8"}

   🔧 Tool calls detected:
      - Tool: weather
      - Args: {"city":"Beijing"}

   🔄 Before Tool Callback:
      - Tool: weather
      - Args: {"city":"Beijing"}
      ✅ Proceeding with normal tool execution

   🔄 After Tool Callback:
      - Tool: weather
      ✅ Tool execution successful, result: {Weather:Sunny, 25°C}
      📊 Adding metadata to weather result

   ✅ Tool response: {"weather":"Sunny, 25°C","metadata":{"source":"callback","timestamp":"2024-01-01T12:00:00Z"}}

   🔄 After Model Callback:
      ✅ Model call successful, choices: 1

   🔄 After Agent Callback:
      ✅ Agent execution completed successfully

   🤖 Assistant: Based on the calculations and weather data...

   ✨ Example completed!
   ```

## Conclusion

Callbacks provide a powerful and flexible way to customize agent behavior at every stage of execution. By understanding and properly implementing these 6 types of callbacks, you can build robust, efficient, and maintainable agent systems that meet your specific requirements.

The key is to use callbacks judiciously - they should enhance functionality without significantly impacting performance or complicating the codebase.
