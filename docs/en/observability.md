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

```go
import (
    "context"
    "encoding/base64"
    "fmt"
    "log"

    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func main() {
    // https://langfuse.com/integrations/native/opentelemetry
    langFuseSecretKey := getEnv("LANGFUSE_SECRET_KEY", "sk-*")
    langFusePublicKey := getEnv("LANGFUSE_PUBLIC_KEY", "pk-*")
    langFuseHost := getEnv("LANGFUSE_HOST", "http://localhost:3000")
    otelEndpointPath := "/api/public/otel/v1/traces"

    // Start tracing
    clean, err := atrace.Start(
        context.Background(),
        atrace.WithEndpointURL(langFuseHost+otelEndpointPath),
        atrace.WithProtocol("http"),
        atrace.WithHeaders(map[string]string{
            "Authorization": fmt.Sprintf("Basic %s", encodeAuth(langFusePublicKey, langFuseSecretKey)),
        }),
    )
    if err != nil {
        log.Fatalf("Failed to start tracing: %v", err)
    }
    defer func() {
        if err := clean(); err != nil {
            log.Printf("Failed to cleanup tracing: %v", err)
        }
    }()
    // ...your Agent application code...
}

func encodeAuth(pk, sk string) string {
    auth := pk + ":" + sk
    return base64.StdEncoding.EncodeToString([]byte(auth))
}
```

See the complete example at [examples/telemetry/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/telemetry/langfuse).

Run the example:

```bash
go run .
```

You can view tracing data in the Langfuse console.

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
    metricClean, err := ametric.Start(
        context.Background(),
        ametric.WithEndpoint("localhost:4317"), // Metric export address.
    )
    if err != nil {
        log.Fatalf("Failed to start metric telemetry: %v", err)
    }
    defer metricClean()

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
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
    "go.opentelemetry.io/otel/trace"
)

func processAgentRequest(ctx context.Context) error {
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
    requestCounter, err := ametric.Meter.Int64Counter(
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
