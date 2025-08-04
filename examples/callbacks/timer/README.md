# Tool Timer Example

This example demonstrates how to use **ToolCallbacks**, **AgentCallbacks**, and **ModelCallbacks** to measure execution time for different components in the agent system.

## Overview

The timer example shows how to implement comprehensive timing measurements across three key components:

- **Agent Timing**: Measures the total time for agent execution
- **Model Timing**: Measures the time for LLM model inference
- **Tool Timing**: Measures the time for individual tool execution

## Key Features

- **Multi-level Timing**: Track performance at agent, model, and tool levels
- **Real-time Output**: See timing information as components execute
- **Interactive Chat**: Test timing with different calculation scenarios
- **Clean Output**: Clear timing indicators with emoji for easy identification

## Timing Output Example

```
⏱️  BeforeAgentCallback: tool-timer-assistant started at 11:05:53.759
   InvocationID: invocation-eb2987aa-6e74-4dc2-862b-795bf1c9fc2b
   UserMsg: "calculate 10 + 20"

⏱️  BeforeModelCallback: model started at 11:05:53.760
   ModelKey: model_1754276753760058036
   Messages: 2

⏱️  AfterModelCallback: model completed in 5.965324643s

⏱️  BeforeToolCallback: fast_calculator started at 11:05:59.725
   Args: {"a":10,"b":20,"operation":"add"}

⏱️  AfterToolCallback: fast_calculator completed in 28.224µs
   Result: {add 10 20 30}
```

## Implementation Details

### Timer Storage Strategy

Since callback interfaces don't support returning modified context, we use instance variables to store timing information:

```go
type toolTimerExample struct {
    toolStartTimes  map[string]time.Time
    agentStartTimes map[string]time.Time
    modelStartTimes map[string]time.Time
    currentModelKey string
}
```

### Callback Registration

The example registers callbacks for all three levels:

```go
// Tool callbacks
toolCallbacks := tool.NewCallbacks()
toolCallbacks.RegisterBeforeTool(e.createBeforeToolCallback())
toolCallbacks.RegisterAfterTool(e.createAfterToolCallback())

// Agent callbacks
agentCallbacks := agent.NewCallbacks()
agentCallbacks.RegisterBeforeAgent(e.createBeforeAgentCallback())
agentCallbacks.RegisterAfterAgent(e.createAfterAgentCallback())

// Model callbacks
modelCallbacks := model.NewCallbacks()
modelCallbacks.RegisterBeforeModel(e.createBeforeModelCallback())
modelCallbacks.RegisterAfterModel(e.createAfterModelCallback())
```

## Running the Example

1. **Set up your API key:**

   ```bash
   export OPENAI_API_KEY="your-api-key"
   ```

2. **Run the example:**

   ```bash
   go run main.go
   ```

3. **Test different scenarios:**
   - `calculate 123 * 321` - Test fast calculator
   - `calculate 10 + 20` - Test basic arithmetic
   - `/history` - Show conversation history
   - `/new` - Start a new session
   - `/exit` - End the conversation

## Available Tools

- **fast_calculator**: Quick calculations (add, subtract, multiply, divide)
- **slow_calculator**: Calculations with artificial 2-second delay

## Performance Insights

From the timing output, you can observe:

- **Model Inference**: Usually the slowest component (4-6 seconds)
- **Tool Execution**: Very fast for local calculations (20-50 microseconds)
- **Agent Overhead**: Minimal additional time beyond model + tool execution

## Use Cases

- **Performance Monitoring**: Identify bottlenecks in your agent pipeline
- **Debugging**: Understand where time is spent in complex workflows
- **Optimization**: Measure the impact of different model configurations
- **Development**: Verify that tools and models are performing as expected

## Customization

To add timing to your own agent system:

1. **Copy the timer structure** from this example
2. **Register callbacks** in your agent setup
3. **Customize timing logic** as needed for your use case
4. **Add additional metrics** like memory usage, API calls, etc.

## Related Examples

- [Multi-turn Chat with Callbacks](../main.go) - Comprehensive callback examples
- [Runner Examples](../../runner/) - Basic agent and tool usage

---

This example demonstrates how to implement comprehensive timing measurements in a production-ready agent system.
