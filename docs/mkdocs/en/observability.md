# Observability Features

## Overview

tRPC-Agent-Go provides comprehensive observability features built on the OpenTelemetry standard, offering powerful observability capabilities for Agent applications. With observability enabled, developers can achieve end-to-end monitoring of Agent runtime status, including tracing, performance metrics collection, and logging.

### 🎯 Key Features

- **Tracing**: Fully records call chains during Agent execution.
- **Metrics**: Collects key runtime performance data for Agents.
- **Logging**: Unified log collection and management.
- **Multi-platform Support**: Supports mainstream monitoring platforms such as Jaeger, Prometheus, Galileo, and ZhiYan Monitoring Bao.
- **Flexible Configuration**: Supports multiple configuration methods and custom extensions.

## Integration with Different Monitoring Platforms

### Langfuse Integration

Langfuse is an observability platform designed for LLM applications and supports collecting tracing data via the OpenTelemetry protocol. tRPC-Agent-Go can export Trace data to Langfuse via OpenTelemetry.

#### 1. Deploy Langfuse

Refer to the Langfuse self-hosting guide for local or cloud deployment. For a quick start, see the Docker Compose deployment guide.

#### 2. Go Code Integration Example

```bash
export LANGFUSE_PUBLIC_KEY="your-public-key"
export LANGFUSE_SECRET_KEY="your-secret-key"
export LANGFUSE_HOST="your-langfuse-host" # In host:port format (no scheme), e.g. "cloud.langfuse.com:443" or "localhost:3000".
export LANGFUSE_INSECURE="true" # Use "true" for local http (development only).
```

```go
import (
	"context"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

func main() {
	// Start trace with Langfuse integration using environment variables
	clean, err := langfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}
	defer func() {
		if err := clean(context.Background()); err != nil {
			log.Printf("Failed to clean up trace telemetry: %v", err)
		}
	}()
```

See the complete example at [examples/telemetry/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/telemetry/langfuse).

Note: `LANGFUSE_HOST` is passed to OpenTelemetry `otlptracehttp.WithEndpoint`, so it must not include `http://` or `https://`. The scheme is controlled by `LANGFUSE_INSECURE`, and the path is fixed to `/api/public/otel/v1/traces`.

Run the example:

```bash
go run .
```

You can view tracing data in the Langfuse console.

For message payload compatibility and the recommended OTel `role + parts` fields, see [Multimodal Telemetry Messages](telemetry-multimodal.md).

#### Skill Observability

When `skill_load` activates a skill, tRPC-Agent-Go emits a Galileo-compatible `invoke_skill {skill.name}` INTERNAL child span under the existing `execute_tool skill_load` span. The `invoke_skill` span represents skill activation/materialization: validating the selected skill and materializing `SKILL.md` content into agent context. It does not cover later `chat`, `workspace_exec`, `skill_run`, `skill_exec`, or other tool execution.

The same activation also records `gen_ai.request_cnt` and `gen_ai.client.operation.duration` on the `trpc_agent_go.internal.invoke_skill` meter. Metric attributes are limited to low-cardinality fields such as `gen_ai.operation.name`, `gen_ai.skill.name`, `gen_ai.skill.id`, `gen_ai.user.id`, `gen_ai.agent.id`, optional `gen_ai.agent.name`, optional `gen_ai.skill.version`, and `error.type` on failures. Paths, content hashes, document lists, tool call IDs, and prompt content are not metric attributes.

By default, the Langfuse exporter drops `invoke_skill` spans so existing Langfuse observation types, tree shape, and token accounting stay unchanged. Galileo and generic OTel exporters can still consume the span and metrics.

For privacy, the `gen_ai.invoke_skill_request` / `gen_ai.invoke_skill_response` span attributes use safe path/content summaries by default: a compact path representation, SHA-256 hashes, byte sizes, and a truncated content preview. Galileo exporters may convert these attributes into platform events for display. Use the span attribute policy to drop, omit, or further truncate these attributes when exporting sensitive skill repositories.

##### Integration Code Description
Langfuse supports receiving Trace data via the `/api/public/otel` (OTLP) endpoint, supporting HTTP/protobuf only, not gRPC.
The above code integrates with Langfuse by setting `OTEL_EXPORTER_OTLP_HEADERS` and `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`.

```bash
# EU data region
OTEL_EXPORTER_OTLP_ENDPOINT="https://cloud.langfuse.com/api/public/otel"
# US data region
# OTEL_EXPORTER_OTLP_ENDPOINT="https://us.cloud.langfuse.com/api/public/otel"
# Local deployment (>= v3.22.0)
# OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:3000/api/public/otel"

# Set Basic Auth authentication
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ${AUTH_STRING}"
```

`AUTH_STRING` is the base64 encoding of `public_key:secret_key`, which can be generated using the following command:

```bash
echo -n "pk-lf-xxxx:sk-lf-xxxx" | base64
# On GNU systems, add -w 0 to avoid line breaks
```

To specify the endpoint for traces only, set:

```bash
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://localhost:3000/api/public/otel/v1/traces"
```


### Jaeger, Prometheus, and Other Open-Source Monitoring Platforms

Refer to code examples in examples/telemetry.

```go
package main

import (
    "context"
    "log"
    
    ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func main() {
    // Start metrics collection.
    mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)

    // Start tracing.
    traceClean, err := atrace.Start(
        context.Background(),
        atrace.WithEndpoint("localhost:4317"), // Trace export address.
    )
    if err != nil {
        log.Fatalf("Failed to start trace telemetry: %v", err)
    }
    defer traceClean()

    // Your Agent application code.
    // ...
    // You can add custom traces and metrics.
}
```

#### Jaeger trace example
![trace-jaeger](../assets/img/telemetry/jaeger.png)

#### Prometheus metrics example

![metric-prometheus](../assets/img/telemetry/prometheus.png)

## Practical Application Examples

### Basic Metrics and Tracing

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
    "trpc.group/trpc-go/trpc-agent-go/log"
    
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
    "go.opentelemetry.io/otel/trace"
)

func main() {
	mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)
	meter := mp.Meter("trpc_agent_go.app")

	if err := processAgentRequest(context.Background(), meter); err != nil {
		log.Errorf("processAgentRequest failed: %v", err)
	}
}

func processAgentRequest(ctx context.Context, meter metric.Meter) error {
    // Create tracing span.
    ctx, span := atrace.Tracer.Start(
        ctx,
        "process-agent-request",
        trace.WithAttributes(
            attribute.String("agent.type", "chat"),
            attribute.String("user.id", "user123"),
        ),
    )
    defer span.End()
    
    // Create metrics counter.
    requestCounter, err := meter.Int64Counter(
        "agent.requests.total",
        metric.WithDescription("Total number of agent requests"),
    )
    if err != nil {
        return err
    }
    
    // Record request.
    requestCounter.Add(ctx, 1, metric.WithAttributes(
        attribute.String("agent.type", "chat"),
        attribute.String("status", "success"),
    ))
    
    // Simulate processing.
    time.Sleep(100 * time.Millisecond)
    
    return nil
}
```

### Agent Execution Tracing

The framework automatically instruments key components of Agents:

```go
// Agent execution will automatically generate the following observability data:
// 
// Traces:
// - agent.execution: Overall Agent execution process.
// - tool.invocation: Tool invocation process.  
// - model.api_call: Model API call process.
```

## Telemetry Data Analysis

### Trace Analysis

A typical Agent execution trace structure:

```
Agent Request
├── Planning Phase
│   ├── Model API Call (DeepSeek)
│   └── Response Processing
├── Tool Execution Phase  
│   ├── Tool: web_search
│   ├── Tool: knowledge_base
│   └── Result Processing
└── Response Generation Phase
    ├── Model API Call (DeepSeek)
    └── Final Response Formatting
```

Trace data can be used to analyze:

- **Performance Bottlenecks**: Identify the most time-consuming operations.
- **Error Localization**: Quickly locate the exact failing step.
- **Dependencies**: Understand relationships between components.
- **Concurrency Analysis**: Observe the effects of concurrent execution.

## Span Attribute Policy (Production Side)

`telemetry/trace` provides an opt-in `SpanAttributePolicy` that controls collection and size of large payload **span attributes** at span creation time.

Default (unset) behavior is unchanged.

### Two capabilities

- `Drop()` and unconditional `Omit()` (without `MaxBytes`) skip `json.Marshal`.
- `MaxBytes()` + `Omit()`: raw `[]byte` paths (for example `execute_tool` tool arguments) compare length without an extra marshal; **JSON-backed** paths (chat / workflow / invoke_agent) still require a full marshal and **do not** reduce heap peaks.
- `Truncate()` always marshals the full payload; it only limits the value written to the span.

| Rule | Reduces marshal heap peak? | Notes |
|------|----------------------------|-------|
| `Drop()` | Yes | Skips marshaling; attribute not written |
| `Omit()` (no `MaxBytes`) | Yes | Skips payload marshaling |
| `MaxBytes(n)` + `Omit()` | JSON: no; `[]byte`: yes | JSON paths marshal fully before comparing size; `[]byte` paths use `len` only |
| `Truncate(n)` | No | Full marshal, then truncate on export |

To reduce memory, prefer `Drop()` on attributes you do not need (for example redundant `*.otel`).

### Coverage

Large payload attributes are wired for `chat`, `invoke_agent`, `workflow`, and `execute_tool` (messages, llm request/response, workflow request/response, tool arguments/result, and related keys).

### Configuration

```go
import atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

clean, err := atrace.Start(ctx,
    atrace.WithSpanAttributePolicy(
        atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrInputMessagesOTel, atrace.Drop()),
        atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrOutputMessagesOTel, atrace.Drop()),
        atrace.WithAttributeRule(atrace.OperationInvokeAgent, atrace.AttrInputMessagesOTel, atrace.Drop()),
    ),
)
```

If `trace.Start` is not used, call `atrace.SetSpanAttributePolicy(...)` before the first LLM invocation. `trace.Start` installs the policy only after tracer initialization succeeds; `clean()` restores the previous policy.

`MaxBytes` + `Omit()` example (limits attribute size; JSON paths still marshal):

```go
atrace.WithAttributeRule(atrace.OperationWorkflow, atrace.AttributeKey("gen_ai.workflow.request"),
    atrace.MaxBytes(16<<10), atrace.Omit(),
)
```

`Truncate` example (full marshal, limited export size):

```go
atrace.WithAttributeRule(atrace.OperationWorkflow, atrace.AttributeKey("gen_ai.workflow.response"), atrace.Truncate(64<<10))
```

### Compatibility

Drop/Omit/Truncate may prevent some backends from reconstructing structured full text from attributes. Opt in based on your backend and memory budget.

## Advanced Features

### Custom Exporter

If you need to send observability data to a custom monitoring system:

```go
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/trace"
)

func setupCustomExporter() error {
    exporter, err := otlptracehttp.New(
        context.Background(),
        otlptracehttp.WithEndpoint("https://your-custom-endpoint.com"),
        otlptracehttp.WithHeaders(map[string]string{
            "Authorization": "Bearer your-token",
        }),
    )
    if err != nil {
        return err
    }
    
    tp := trace.NewTracerProvider(
        trace.WithBatcher(exporter),
    )
    
    // Set as the global TracerProvider.
    otel.SetTracerProvider(tp)
    
    return nil
}
```

## References

- OpenTelemetry documentation.
- tRPC-Agent-Go telemetry examples.

By using observability features properly, you can establish a complete monitoring system for Agent applications, discover and resolve issues in time, and continuously optimize system performance.
