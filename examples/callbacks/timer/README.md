# Tool Timer with Telemetry Example

This example demonstrates how to use **ToolCallbacks**, **AgentCallbacks**, and **ModelCallbacks** to measure execution time for different components in the agent system and report the data to **OpenTelemetry** for monitoring and observability.

## Overview

The timer example shows how to implement comprehensive timing measurements across three key components with telemetry integration:

- **Agent Timing**: Measures the total time for agent execution
- **Model Timing**: Measures the time for LLM model inference
- **Tool Timing**: Measures the time for individual tool execution
- **Telemetry Integration**: Reports metrics and traces to OpenTelemetry Collector

## File Structure

- `main.go`: Entry point, initializes telemetry, wires model, tools, callbacks, runner (in-memory session), and CLI
- `callbacks.go`: Callback registrations and timing/telemetry logic (Agent/Model/Tool Before/After)
- `tools.go`: Tool implementations (`calculator`) and its data types
- `docker-compose.yaml`, `otel-collector.yaml`, `prometheus.yaml`: Local telemetry stack

## Key Features

- **Multi-level Timing**: Track performance at agent, model, and tool levels
- **Real-time Output**: See timing information as components execute
- **Interactive Chat**: Test timing with different calculation scenarios
- **Clean Output**: Clear timing indicators with emoji for easy identification
- **OpenTelemetry Integration**: Automatic reporting to Jaeger (traces) and Prometheus (metrics)
- **Observability**: View traces in Jaeger UI and metrics in Prometheus
- **In-memory Session**: Uses in-memory session service for simplicity

## Architecture

```
Timer Example + OpenTelemetry Integration
┌─────────────────┐    ┌─────────────────────┐    ┌─────────────┐
│   Timer App     │───▶│  OTEL Collector     │───▶│   Jaeger    │
│   (main.go)     │    │  (localhost:4317)   │    │ (localhost: │
│                 │    │                     │    │   16686)    │
└─────────────────┘    └─────────────────────┘    └─────────────┘
                             │
                             ▼
                      ┌─────────────┐
                      │ Prometheus  │
                      │ (localhost: │
                      │    9090)    │
                      └─────────────┘
```

## Timing Output Example

```
⏱️  BeforeAgentCallback: tool-timer-assistant started at 11:05:53.759
   InvocationID: invocation-...
   UserMsg: "calculate 10 + 20"

⏱️  BeforeModelCallback: model started at 11:05:53.760
   ModelKey: model_1754276753760058036
   Messages: 2

⏱️  AfterModelCallback: model completed in 5.965324643s

⏱️  BeforeToolCallback: calculator started at 11:05:59.725
   Args: {"a":10,"b":20,"operation":"add"}

⏱️  AfterToolCallback: calculator completed in 28.224µs
   Result: {add 10 20 30}
```

## Telemetry Data

The example automatically reports the following telemetry data:

### Metrics (Prometheus)

- `agent_duration_seconds` - Histogram of agent execution times
- `model_duration_seconds` - Histogram of model inference times
- `tool_duration_seconds` - Histogram of tool execution times
- `agent_executions_total` - Counter of agent executions
- `model_inferences_total` - Counter of model inferences
- `tool_executions_total` - Counter of tool executions

### Traces (Jaeger)

- `agent_execution` - Trace spans for agent execution
- `model_inference` - Trace spans for model inference
- `tool_execution` - Trace spans for tool execution

Each trace includes attributes like:

- Component name (agent.name, tool.name)
- Duration in seconds
- Status (success/error)
- Additional context (invocation ID, arguments, etc.)

## Implementation Details

### Timer Storage Strategy

This example demonstrates the use of **Invocation State** for storing timing information. Instead of using instance variables, we leverage the `Invocation.SetState()`, `Invocation.GetState()`, and `Invocation.DeleteState()` methods to share data between Before and After callbacks.

**Key Benefits:**

- **Invocation-scoped**: State is automatically scoped to a single invocation
- **Thread-safe**: Built-in concurrency protection with RWMutex
- **Clean lifecycle**: State is cleaned up after use, no memory leaks
- **No external storage**: No need for instance variables or maps

**State Key Convention:**

- Agent callbacks: `"agent:xxx"` (e.g., `"agent:start_time"`)
- Model callbacks: `"model:xxx"` (e.g., `"model:start_time"`)
- Tool callbacks: `"tool:<toolName>:<toolCallID>:xxx"` (e.g., `"tool:calculator:call_abc123:start_time"`)

For Model and Tool callbacks, retrieve the invocation from context using `agent.InvocationFromContext(ctx)`.

**Handling Concurrent Tool Calls:**

When LLMs return multiple tool calls in a single response (including multiple calls to the same tool), the framework executes them concurrently. To correctly track timing and spans for each tool call:

1. **Get tool call ID from context**: Use `tool.ToolCallIDFromContext(ctx)` to retrieve the unique ID for each tool call
2. **Use tool call ID in state keys**: Include the tool call ID in your state keys to ensure uniqueness
3. **Fallback handling**: If tool call ID is not available, use `"default"` as a fallback

Example:

```go
// Get tool call ID for concurrent tool call support
toolCallID, ok := tool.ToolCallIDFromContext(ctx)
if !ok || toolCallID == "" {
    toolCallID = "default"  // Fallback for compatibility
}

// Use tool call ID in state keys
key := fmt.Sprintf("tool:%s:%s:start_time", toolName, toolCallID)
inv.SetState(key, startTime)
```

This ensures that when the LLM calls `calculator` multiple times concurrently (e.g., `calculator(1,2)` and `calculator(3,4)`), each call has its own independent timing and span data.

### Callback Registration

Callbacks are registered in `callbacks.go`:

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

### Telemetry Integration

Each callback creates OpenTelemetry spans and records metrics (see `callbacks.go`).

## Running the Example

### Prerequisites

1. **Install Docker Compose V2** for running the telemetry infrastructure
2. **Set up your API key:**
   ```bash
   export OPENAI_API_KEY="your-api-key"
   ```

### Start Telemetry Infrastructure

1. **Start the telemetry stack:**

   ```bash
   docker compose up -d
   ```

2. **Verify services are running:**
   - OpenTelemetry Collector: `localhost:4317`
   - Jaeger UI: http://localhost:16686
   - Prometheus: http://localhost:9090

### Run the Timer Example

1. **Run the example:**

   ```bash
   go run .
   ```

2. **Test different scenarios:**
   - `calculate 123 * 321` - Test multiplication
   - `calculate 10 + 20` - Test addition
   - `/history` - Show conversation history
   - `/new` - Start a new session
   - `/exit` - End the conversation

### View Telemetry Data

- Jaeger: http://localhost:16686
- Prometheus: http://localhost:9090

## Available Tools

- **calculator**: Basic operations (add, subtract, multiply, divide)

## Customization

To add timing and telemetry to your own agent system:

1. Use `callbacks.go` as a template to add timing at Agent/Model/Tool levels
2. Register callbacks in your agent setup
3. Add additional metrics or spans as needed
4. Configure telemetry endpoints for your environment

## Related Examples

- [Multi-turn Chat with Callbacks](../main.go) - Comprehensive callback examples
- [Authentication and Authorization](../auth/) - User context and permission checks with Invocation State
- [Telemetry Example](../../telemetry/) - Basic OpenTelemetry integration
- [Runner Examples](../../runner/) - Basic agent and tool usage
