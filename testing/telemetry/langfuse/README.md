# Langfuse Testing Helper

`testing/telemetry/langfuse` is an opt-in helper for troubleshooting telemetry during local runs.
Import it anonymously to auto-start Langfuse from environment variables and an optional YAML config file.
Reporting stays off by default and only starts when explicitly enabled.

## Enable Auto-Start

| Variable | Description | Default Value |
| -------- | ----------- | ------------- |
| `TRPC_AGENT_TELEMETRY_ENABLED` | Set to `true` to enable helper auto-start | `false` |
| `TRPC_AGENT_TELEMETRY_BACKEND` | Backend selector; use `langfuse` when set | `` |
| `TRPC_AGENT_TELEMETRY_CONFIG` | Path to the Langfuse YAML config file | `` |

## YAML Config

Minimal YAML example:

```yaml
enabled: true
public_key: pk-lf-xxxx
secret_key: sk-lf-xxxx
host: localhost:3000
insecure: true
processor: simple
observation_leaf_value_max_bytes: 4096
```

`processor` defaults to `simple`, which is better for troubleshooting because spans are exported synchronously when they end.
Set it to `batch` only if you specifically want the existing asynchronous behavior.

For fields not set in the YAML file, the helper remains compatible with the existing `LANGFUSE_*` environment variables used by `telemetry/langfuse`.

## Usage

Import the helper anonymously:

```go
import _ "trpc.group/trpc-go/trpc-agent-go/testing/telemetry/langfuse"
```

For example, to run `examples/llmagent` with telemetry enabled:

```bash
cd examples/llmagent
export OPENAI_API_KEY="your-api-key"
export TRPC_AGENT_TELEMETRY_ENABLED=true
export TRPC_AGENT_TELEMETRY_BACKEND=langfuse
export TRPC_AGENT_TELEMETRY_CONFIG=./langfuse.yaml
go run .
```
