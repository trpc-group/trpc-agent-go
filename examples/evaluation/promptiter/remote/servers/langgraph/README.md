# LangGraph tRPC-Agent Service

This example exposes a LangGraph sports recap graph through the `server/trpcagent` HTTP protocol so PromptIter can optimize the same stage instruction surfaces as the Go reference server.

It is a text-only PromptIter adapter for the remote PromptIter example.

The stage agents use `deepseek-v3.2` by default with temperature `0.0` and max output tokens `32768`, matching the Go reference service.

## Run

```bash
cd examples/evaluation/promptiter/remote/servers/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_API_KEY="your-openai-compatible-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export LANGGRAPH_MODEL="deepseek-v3.2"
python server.py
```

The service listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/promptiter-sports-recap-agent` by default.

Then run PromptIter from the Go remote example:

```bash
cd ../..
export OPENAI_API_KEY="your-openai-compatible-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . -candidate-target http://127.0.0.1:8081
```
