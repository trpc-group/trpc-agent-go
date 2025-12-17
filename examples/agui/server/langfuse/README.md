# Langfuse AG-UI Server

This example extends the default AG-UI SSE server with Langfuse telemetry. It demonstrates how to stream agent interactions to AG-UI while exporting structured traces to Langfuse using the OpenTelemetry SDK shipped with `tRPC-Agent-Go`.

## Prerequisites

- A Langfuse deployment that accepts OpenTelemetry trace ingestion, either self-hosted or cloud-hosted.
- Langfuse API credentials and host information exported as environment variables before you run the server.
- An AG-UI client, such as the raw CLI client bundled with this repository or the Copilotkit web client in `examples/agui/client/copilotkit`.

Langfuse offers multiple deployment options. See the [official self-hosting guide](https://langfuse.com/self-hosting) for details. For this example, you can quickly get started by [deploying Langfuse locally or on a VM using Docker Compose](https://langfuse.com/self-hosting/docker-compose).

```bash
export LANGFUSE_PUBLIC_KEY="your-public-key"
export LANGFUSE_SECRET_KEY="your-secret-key"
export LANGFUSE_HOST="localhost:3000"          # Langfuse host in host:port format (no http://)
export LANGFUSE_INSECURE="true"                # keep false in production
```

## Run

Navigate to the `examples/agui` module and start the server.

```bash
# Starts the server on http://127.0.0.1:8080/agui by default
go run .
```

The example accepts several flags so you can point at a different model, address, or SSE path.

```bash
go run . -model deepseek-chat -address 0.0.0.0:8080 -path /agui -stream=true
```

On startup you should see a log entry similar to the one below.

```
2025-10-10T17:01:47+08:00       INFO    langfuse/main.go:54      AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
```

## What Happens During a Request

When a client posts an AG-UI run request, the runner calls a `StartSpan` hook (see `startSpan` in `main.go`) to create an OpenTelemetry span enriched with Langfuse-specific attributes such as `langfuse.session.id`, `langfuse.user.id`, and `langfuse.trace.input`. The span context is propagated through the AG-UI runner so every streamed event shares the same trace.

After each event is translated for AG-UI delivery, an `AfterTranslate` callback aggregates incremental text deltas and records the final answer in the span attribute `langfuse.trace.output`. This guarantees that both the user prompt and the final model output appear side by side in Langfuse for easy inspection.

## Observing the Trace in Langfuse

![langfuse](../../../../.resource/images/examples/agui-langfuse.png)
