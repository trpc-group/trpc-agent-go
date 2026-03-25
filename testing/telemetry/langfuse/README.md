# Langfuse Testing Helper

`testing/telemetry/langfuse` is an opt-in helper for troubleshooting telemetry during local runs.
Import it anonymously to auto-start Langfuse from environment variables and an optional YAML config file.
Reporting stays off by default and only starts when explicitly enabled.

## Enable Auto-Start

| Variable | Description | Default Value |
| -------- | ----------- | ------------- |
| `TRPC_AGENT_TELEMETRY_ENABLED` | Set to `true` to enable helper auto-start | `false` |
| `TRPC_AGENT_TELEMETRY_CONFIG` | Path to the telemetry YAML config file | `` |

## YAML Config

Use `examples/langfuse.yaml` as the canonical config example for this helper.
It uses the current backend-oriented schema, with a top-level `backend` selector and a matching `langfuse` config block.

`processor` defaults to `simple`, which is better for troubleshooting because spans are exported synchronously when they end.
Set it to `batch` only if you specifically want the existing asynchronous behavior.

The config file must declare the top-level `backend` selector and the matching `langfuse` config block.
This keeps the schema backend-oriented so it can be extended to future backends such as LangSmith or Jaeger without changing the top-level shape.

The example file leaves `public_key` and `secret_key` empty on purpose.
You can either fill them in directly or continue using the existing `LANGFUSE_*` environment variables from `telemetry/langfuse`.
For local development, you can also create `examples/langfuse.local.yaml`; the bundled scripts will prefer it automatically, and it is intended to stay uncommitted.

## Usage

Import the helper anonymously:

```go
import _ "trpc.group/trpc-go/trpc-agent-go/testing/telemetry/langfuse"
```

The bundled script `examples/run-examples.sh` exports `TRPC_AGENT_TELEMETRY_ENABLED=true`, prefers `examples/langfuse.local.yaml` when present, otherwise falls back to `examples/langfuse.yaml`, and runs `examples/llmagent` by default.
It only configures telemetry. The target example still needs model-side environment variables such as `OPENAI_API_KEY` and `OPENAI_BASE_URL`.
For the default `deepseek-chat` model in `examples/llmagent`, `OPENAI_BASE_URL` should point to your OpenAI-compatible provider endpoint.

To run it:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
bash testing/telemetry/langfuse/examples/run-examples.sh
```

To target a different example, pass the example directory as the first argument:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
bash testing/telemetry/langfuse/examples/run-examples.sh ./examples/callbacks/timer
```

## Local Deployment

For a local self-hosted Langfuse instance, use the gitignored `deployment/` workspace.
It follows the [official Docker Compose deployment guide](https://langfuse.com/self-hosting/deployment/docker-compose), but the helper scripts standardize on a local `docker-compose` command name.

To prepare and start the stack:

```bash
bash testing/telemetry/langfuse/deployment/sync-langfuse.sh
# edit testing/telemetry/langfuse/deployment/langfuse/docker-compose.yml and replace secrets marked # CHANGEME
bash testing/telemetry/langfuse/deployment/up.sh
```

After the containers are ready, open `http://localhost:3000`.
To stop the local deployment:

```bash
bash testing/telemetry/langfuse/deployment/down.sh
```
