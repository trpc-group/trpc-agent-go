# LangGraph tRPC-Agent Service

This example exposes a LangGraph graph through the `server/trpcagent` HTTP protocol so PromptIter can optimize its `candidate#instruction` surface remotely.

It is a text-only PromptIter adapter, not a full protocol reference implementation. Use the Go candidate server when you need complete `server/trpcagent` behavior.

## Run

```bash
cd examples/evaluation/promptiter/remote/servers/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_API_KEY="your-openai-key"
export LANGGRAPH_MODEL="openai:gpt-5.2"
python server.py
```

The service listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/promptiter-nba-commentary-candidate` by default.

Then run PromptIter from the Go remote example:

```bash
cd ../..
export OPENAI_API_KEY="your-openai-compatible-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . -mode run -candidate-target http://127.0.0.1:8081
```
