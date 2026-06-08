# OpenViking Tools Example

This example shows how to use the OpenViking context database
([volcengine/OpenViking](https://github.com/volcengine/OpenViking)) as a set of
agent tools via `tool/openviking`.

The agent follows OpenViking's native **search then read** pattern:

1. `viking_search` / `viking_find` locate relevant `viking://` URIs (returning
   short summaries, not full content).
2. `viking_read` fetches full content (L2) only for the URIs the model decides
   it needs — this is what keeps token usage low.

## Prerequisites

1. A running OpenViking server, reachable at `http://localhost:1933` by default:

   ```bash
   pip install openviking
   openviking-server init    # configure embedding + VLM providers
   openviking-server         # start the server
   ```

   Optionally ingest something to query:

   ```bash
   ov add-resource https://github.com/volcengine/OpenViking --wait
   ```

2. An OpenAI-compatible model endpoint. Set the usual environment variables:

   ```bash
   export OPENAI_API_KEY="your-key"
   export OPENAI_BASE_URL="https://your-endpoint/v1"   # if not using api.openai.com
   ```

## Run

```bash
go run . -model deepseek-v4-flash -openviking http://localhost:1933 -profile agent
```

Flags:

- `-model`: model name (default `deepseek-v4-flash`).
- `-openviking`: OpenViking server URL (default `http://localhost:1933`).
- `-openviking-key`: API key (defaults to `OPENVIKING_API_KEY`).
- `-profile`: tool profile — `retrieval` (read-only), `agent` (default), or `admin`.

## Tool profiles

| Profile | Tools |
|---|---|
| `retrieval` | viking_find, viking_search, viking_browse, viking_read, viking_grep, viking_health |
| `agent` | retrieval + viking_store, viking_add_resource, viking_add_skill |
| `admin` | agent + viking_forget (destructive) |
