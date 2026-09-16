# LangGraph tRPC-Agent Server

This is the LangGraph candidate server for `examples/evaluation/trpcagent`. It builds a real `StateGraph`, calls an OpenAI-compatible LLM through `ChatOpenAI`, and exposes the same HTTP protocol used by `server/trpcagent`.

## Run

```bash
cd examples/evaluation/trpcagent/servers/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export LANGGRAPH_MODEL="gpt-5.2"
python server.py
```

`OPENAI_BASE_URL` is optional when the provider uses the default OpenAI endpoint.

The server listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/trpcagent-travel-agent` by default. Its `/runs` response includes an execution trace when the client sends `executionTraceEnabled: true`; token usage is included when LangChain returns usage metadata.
