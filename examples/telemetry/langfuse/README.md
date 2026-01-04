# Langfuse Collector

trpc-agent-go uses OpenTelemetry to collect traces and supports exporting trace data to Langfuse.

## Deployment Langfuse

Langfuse offers multiple deployment options. See the [official self-hosting guide](https://langfuse.com/self-hosting) for details.

For this example, you can quickly get started by [deploying Langfuse locally or on a VM using Docker Compose](https://langfuse.com/self-hosting/docker-compose).


## Environment Variables Configuration

```bash
export LANGFUSE_PUBLIC_KEY="your-public-key"
export LANGFUSE_SECRET_KEY="your-secret-key"
export LANGFUSE_HOST="your-langfuse-host" # In host:port format (no scheme), e.g. "cloud.langfuse.com:443" or "localhost:3000".
export LANGFUSE_INSECURE="true" # Use "true" for local http (development only).
```

Note: `LANGFUSE_HOST` is passed to OpenTelemetry `otlptracehttp.WithEndpoint`, so it must not include `http://` or `https://`. The scheme is controlled by `LANGFUSE_INSECURE`, and the path is fixed to `/api/public/otel/v1/traces`.

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

## Running the code

You can find the complete code for this example in the [main.go](./main.go)
file. To run it, ensure you have a somewhat recent version of Go (preferably >=
1.13) and do

```bash
go run .
```

The example simulates an intelligent agent application that processes a series of user messages, demonstrating tracing and metrics collection for multiple tool-based tasks.

## Viewing Trace data

![telemetry-langfuse-trace](../../../.resource/images/examples/telemetry-langfuse-trace.png)
