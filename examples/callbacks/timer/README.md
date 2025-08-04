# Tool Timer with Telemetry Example

This example demonstrates how to use **ToolCallbacks**, **AgentCallbacks**, and **ModelCallbacks** to measure execution time for different components in the agent system and report the data to **OpenTelemetry** for monitoring and observability.

## Overview

The timer example shows how to implement comprehensive timing measurements across three key components with telemetry integration:

- **Agent Timing**: Measures the total time for agent execution
- **Model Timing**: Measures the time for LLM model inference
- **Tool Timing**: Measures the time for individual tool execution
- **Telemetry Integration**: Reports metrics and traces to OpenTelemetry Collector

## Key Features

- **Multi-level Timing**: Track performance at agent, model, and tool levels
- **Real-time Output**: See timing information as components execute
- **Interactive Chat**: Test timing with different calculation scenarios
- **Clean Output**: Clear timing indicators with emoji for easy identification
- **OpenTelemetry Integration**: Automatic reporting to Jaeger (traces) and Prometheus (metrics)
- **Observability**: View traces in Jaeger UI and metrics in Prometheus

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

Since callback interfaces don't support returning modified context, we use instance variables to store timing information:

```go
type toolTimerExample struct {
    toolStartTimes  map[string]time.Time
    agentStartTimes map[string]time.Time
    modelStartTimes map[string]time.Time
    currentModelKey string
    // Telemetry metrics
    agentDurationHistogram   metric.Float64Histogram
    toolDurationHistogram    metric.Float64Histogram
    modelDurationHistogram   metric.Float64Histogram
    agentCounter            metric.Int64Counter
    toolCounter             metric.Int64Counter
    modelCounter            metric.Int64Counter
}
```

### Callback Registration

The example registers callbacks for all three levels with telemetry integration:

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

Each callback creates OpenTelemetry spans and records metrics:

```go
// Create trace span
ctx, span := atrace.Tracer.Start(ctx, "agent_execution",
    trace.WithAttributes(
        attribute.String("agent.name", invocation.AgentName),
        attribute.String("invocation.id", invocation.InvocationID),
    ),
)

// Record metrics
e.agentDurationHistogram.Record(ctx, durationSeconds,
    metric.WithAttributes(
        attribute.String("agent.name", invocation.AgentName),
    ),
)
```

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
   go run main.go
   ```

2. **Test different scenarios:**
   - `calculate 123 * 321` - Test fast calculator
   - `calculate 10 + 20` - Test basic arithmetic
   - `/history` - Show conversation history
   - `/new` - Start a new session
   - `/exit` - End the conversation

### View Telemetry Data

1. **View traces in Jaeger:**

   - Open http://localhost:16686
   - Search for service "telemetry"
   - View trace spans for agent, model, and tool execution

2. **View metrics in Prometheus:**
   - Open http://localhost:9090
   - In the query input field, search for metrics like:
     - `agent_duration_seconds` - Agent execution duration histogram
     - `model_duration_seconds` - Model inference duration histogram
     - `tool_duration_seconds` - Tool execution duration histogram
     - `agent_executions_total` - Total agent executions counter
     - `model_inferences_total` - Total model inferences counter
     - `tool_executions_total` - Total tool executions counter
   - Click "Execute" to see the results
   - Switch to "Graph" tab to visualize the metrics over time

### Prometheus Query Examples

Here are some useful PromQL queries you can try:

**Duration Histograms:**

```
# Agent execution duration (95th percentile)
histogram_quantile(0.95, agent_duration_seconds_bucket)

# Model inference duration (average)
rate(model_duration_seconds_sum[5m]) / rate(model_duration_seconds_count[5m])

# Tool execution duration (max)
max(tool_duration_seconds)
```

**Execution Counters:**

```
# Total agent executions
agent_executions_total

# Rate of model inferences per minute
rate(model_inferences_total[1m])

# Tool executions by tool name
tool_executions_total
```

**Error Rates:**

```
# Agent error rate
rate(agent_executions_total{status="error"}[5m]) / rate(agent_executions_total[5m])
```

## Available Tools

- **fast_calculator**: Quick calculations (add, subtract, multiply, divide)
- **slow_calculator**: Calculations with artificial 2-second delay

## Performance Insights

From the timing output and telemetry data, you can observe:

- **Model Inference**: Usually the slowest component (4-6 seconds)
- **Tool Execution**: Very fast for local calculations (20-50 microseconds)
- **Agent Overhead**: Minimal additional time beyond model + tool execution
- **Telemetry Overhead**: Minimal impact on performance

## Use Cases

- **Performance Monitoring**: Identify bottlenecks in your agent pipeline
- **Debugging**: Understand where time is spent in complex workflows
- **Optimization**: Measure the impact of different model configurations
- **Development**: Verify that tools and models are performing as expected
- **Production Monitoring**: Use telemetry data for alerting and dashboards
- **Distributed Tracing**: Track requests across multiple services

## Customization

To add timing and telemetry to your own agent system:

1. **Copy the timer structure** from this example
2. **Register callbacks** in your agent setup
3. **Customize timing logic** as needed for your use case
4. **Add additional metrics** like memory usage, API calls, etc.
5. **Configure telemetry endpoints** for your environment
6. **Set up monitoring dashboards** using the telemetry data

## Troubleshooting

### No metrics found in Prometheus?

1. **Check if metrics are being sent:**

   - Look for "No data queried yet" message
   - Try running the timer example multiple times to generate more data
   - Check the OpenTelemetry Collector logs: `sudo docker compose logs otel-collector`

2. **Check metric names:**

   - The metrics are prefixed with `trpcgoagent_` (from the collector config)
   - Try searching for: `trpcgoagent_agent_duration_seconds`
   - Or use the metric browser in Prometheus UI

3. **Check time range:**
   - Make sure the time range includes when you ran the example
   - Try "Last 1 hour" or "Last 6 hours"

### No traces found in Jaeger?

1. **Check service name:**

   - Look for service "telemetry" (not "timer-example")
   - The service name comes from the OpenTelemetry SDK configuration

2. **Check time range:**
   - Make sure the time range includes when you ran the example

## Shutting Down

To shut down the telemetry infrastructure:

```bash
docker compose down
```

## Related Examples

- [Multi-turn Chat with Callbacks](../main.go) - Comprehensive callback examples
- [Telemetry Example](../../telemetry/) - Basic OpenTelemetry integration
- [Runner Examples](../../runner/) - Basic agent and tool usage

---

This example demonstrates how to implement comprehensive timing measurements with production-ready observability using OpenTelemetry.
