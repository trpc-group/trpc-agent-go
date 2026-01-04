# Graph Activity AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` that executes a small recipe scaling workflow graph.

The graph includes function/LLM/tools/agent nodes to exercise different `GraphAgent` node types in a realistic scenario.

When each graph node starts, the AG-UI translator emits an `ACTIVITY_DELTA` event with:

- `activityType`: `graph.node.start`
- `patch`: sets `/node` to the current node information

This helps the frontend track which node is executing, including during resume-from-interrupt flows.

The node IDs are executed in this order:

- `prepare` (function).
- `recipe_calc_llm` (llm).
- `execute_tools` (tool).
- `confirm` (function, interrupt).
- `draft_message_llm` (llm).
- `polish_message_agent` (agent).
- `finish` (function).

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.
go run ./server/graph \
  -model deepseek-chat \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Verify With curl

First request: the graph will interrupt at `confirm` (after the tool is executed).

```bash
curl --no-buffer --location 'http://127.0.0.1:8080/agui' \
  --header 'Content-Type: application/json' \
  --data '{
    "threadId": "demo-thread",
    "runId": "demo-run-1",
    "forwardedProps": {
      "lineage_id": "demo-lineage"
    },
    "messages": [
      {"role": "user", "content": "Please help me scale a cookie recipe.\\n\\nBase servings: 8\\nDesired servings: 12\\nBase flour (g): 200\\nBase butter (g): 120\\nBase sugar (g): 80\\n\\nPlease calculate the scaled ingredient amounts and wait for my confirmation before writing the final recipe message."}
    ]
  }'
```

Second request: resume from the latest checkpoint in the same lineage by providing `forwardedProps.checkpoint_id=""` and a `forwardedProps.resume_map` value.

```bash
curl --no-buffer --location 'http://127.0.0.1:8080/agui' \
  --header 'Content-Type: application/json' \
  --data '{
    "threadId": "demo-thread",
    "runId": "demo-run-2",
    "forwardedProps": {
      "lineage_id": "demo-lineage",
      "checkpoint_id": "",
      "resume_map": {
        "confirm": true
      }
    },
    "messages": [
      {"role": "user", "content": "resume"}
    ]
  }'
```

Look for SSE `data:` lines that contain `"type":"ACTIVITY_DELTA"`, for example:

```json
{
  "type": "ACTIVITY_DELTA",
  "activityType": "graph.node.start",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "prepare"
      }
    }
  ]
}
```
