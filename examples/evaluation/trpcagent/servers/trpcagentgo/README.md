# Go tRPC-Agent Server

This is the Go candidate server for `examples/evaluation/trpcagent`. It builds a real `llmagent`, uses `gpt-5.2` by default, and exposes the agent through `server/trpcagent`.

## Run

```bash
cd examples/evaluation/trpcagent/servers/trpcagentgo
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . -model "gpt-5.2"
```

`OPENAI_BASE_URL` is optional when the provider uses the default OpenAI endpoint.

The server listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/trpcagent-travel-agent` by default. Its `/runs` response includes the native tRPC-Agent execution trace when the client sends `executionTraceEnabled: true`.
