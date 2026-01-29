# Graph Activity AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` that executes a small recipe scaling workflow graph.

The graph includes function/LLM/tools/agent nodes to exercise different `GraphAgent` node types in a realistic scenario.

The AG-UI server emits `ACTIVITY_DELTA` events that the frontend can use to track graph progress and Human-in-the-Loop interrupts:

- `activityType`: `graph.node.lifecycle` writes the node lifecycle state to `/node` (`nodeId`, `phase=start|complete|error`, `error?`).
- `activityType`: `graph.node.interrupt` writes the interrupt payload to `/interrupt`. The interrupt payload includes `nodeId`, `key`, `prompt`, `checkpointId`, and `lineageId`. `key` and `prompt` are the 3rd and 4th arguments passed to `graph.Interrupt(ctx, state, key, prompt)`.
- Resume ack: on resume runs, the server emits an extra `graph.node.interrupt` event at the beginning of the run. It clears `/interrupt` to `null` and writes the resume input to `/resume`.

These graph activity events are disabled by default. This example enables them via `agui.WithGraphNodeLifecycleActivityEnabled(true)` and `agui.WithGraphNodeInterruptActivityEnabled(true)`.

This helps the frontend track which node is executing and render Human-in-the-Loop prompts, including during resume-from-interrupt flows.

The node IDs are executed in this order:

- `prepare`: function.
- `recipe_calc_llm`: llm.
- `execute_tools`: tool.
- `confirm`: function, interrupt.
- `draft_message_llm`: llm.
- `polish_message_agent`: agent.
- `finish`: function.

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

## Verify with curl

First request: start a fresh run. The graph will interrupt at `confirm` after the tool is executed.

`state.lineage_id` is optional for the first run. If omitted, the server generates one and includes it in the interrupt event payload so you can reuse it for resuming later. `state.checkpoint_id` and `state.resume_map` are only needed for resume runs.

```bash
curl --no-buffer --location 'http://127.0.0.1:8080/agui' \
  --header 'Content-Type: application/json' \
  --data '{
    "threadId": "demo-thread",
    "runId": "demo-run-1",
    "state": {
      "lineage_id": "demo-lineage"
    },
    "messages": [
      {"role": "user", "content": "Please help me scale a cookie recipe.\\n\\nBase servings: 8\\nDesired servings: 12\\nBase flour (g): 200\\nBase butter (g): 120\\nBase sugar (g): 80\\n\\nPlease calculate the scaled ingredient amounts and wait for my confirmation before writing the final recipe message."}
    ]
  }'
```

Second request: resume from a checkpoint in the same lineage.

- Provide `state.lineage_id` and `state.resume_map`.
- `state.checkpoint_id` is optional. Use an empty string (or omit the field) to resume from the latest checkpoint, or set it to the `checkpointId` returned by the interrupt event to resume a specific checkpoint.

```bash
curl --no-buffer --location 'http://127.0.0.1:8080/agui' \
  --header 'Content-Type: application/json' \
  --data '{
    "threadId": "demo-thread",
    "runId": "demo-run-2",
    "state": {
      "lineage_id": "demo-lineage",
      "checkpoint_id": "",
      "resume_map": {
        "confirm": true
      }
    },
    "messages": [
      {"role": "user", "content": ""}
    ]
  }'
```

Look for SSE `data:` lines that contain `"type":"ACTIVITY_DELTA"`, for example:

Node start:

This event is emitted before the node actually runs. It sets `/node.nodeId` (and `/node.phase`) so the frontend can highlight the current node.

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767596081644,
  "messageId": "7e3c1eb2-670f-470d-9a5d-9270207b5c02",
  "activityType": "graph.node.lifecycle",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "confirm",
        "phase": "start"
      }
    }
  ]
}
```

Node complete:

This event is emitted after the node finishes. It updates `/node` with `phase=complete`.

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767596081999,
  "messageId": "f74e9d63-2a77-46a1-8bc6-b2b2f4f84e06",
  "activityType": "graph.node.lifecycle",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "confirm",
        "phase": "complete"
      }
    }
  ]
}
```

Interrupt:

This event is emitted when a node calls `graph.Interrupt(...)` and there is no available resume input. It writes the interrupt payload to `/interrupt`, including `key`/`prompt` and the `checkpointId`/`lineageId` needed for resuming.

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767949904999,
  "messageId": "04222c25-f9e1-44c9-ba32-e2ad874123ce",
  "activityType": "graph.node.interrupt",
  "patch": [
    {
      "op": "add",
      "path": "/interrupt",
      "value": {
        "nodeId": "confirm",
        "key": "confirm",
        "prompt": "Confirm continuing after the recipe amounts are calculated.",
        "checkpointId": "8780b21e-7f38-4224-a5ea-cbb43e6f71bc",
        "lineageId": "demo-lineage"
      }
    }
  ]
}
```

Resume ack:

This event is emitted before any `graph.node.lifecycle` events for the run. It clears `/interrupt` to `null` and writes the resume input to `/resume`. `/resume` contains `resumeMap` or `resume`. It may also include `checkpointId` and `lineageId`.

Example captured from the second request above. `checkpointId` is omitted because `state.checkpoint_id` is an empty string.

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767950998788,
  "messageId": "293cec35-9689-4628-82d3-475cc91dab20",
  "activityType": "graph.node.interrupt",
  "patch": [
    {
      "op": "add",
      "path": "/interrupt",
      "value": null
    },
    {
      "op": "add",
      "path": "/resume",
      "value": {
        "lineageId": "demo-lineage",
        "resumeMap": {
          "confirm": true
        }
      }
    }
  ]
}
```
