# ADK tRPC-Agent Server

This is the ADK candidate server for `examples/evaluation/trpcagent`. It builds a real Python ADK `Agent`, calls an OpenAI-compatible LLM through `LiteLlm`, and exposes the same HTTP protocol used by `server/trpcagent`.

## Run

```bash
cd examples/evaluation/trpcagent/servers/adk
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_API_KEY="your-api-key"
export OPENAI_API_BASE="https://your-openai-compatible-endpoint/v1"
export ADK_MODEL="openai/gpt-5.2"
python server.py
```

`OPENAI_API_BASE` is optional when the provider uses the default OpenAI endpoint.

The server listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/trpcagent-travel-agent` by default. Its `/runs` response includes an execution trace when the client sends `executionTraceEnabled: true`; token usage is included when ADK returns usage metadata.
