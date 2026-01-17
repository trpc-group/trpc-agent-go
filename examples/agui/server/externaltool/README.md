# External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` that demonstrates an external tool workflow.

The workflow can be split into two requests:

- Call 1 (`role=user`): the LLM may emit `TOOL_CALL_*` events. If it calls the tool, the graph interrupts (checkpoints) at the tool node.
- Call 2 (`role=tool`): the caller executes the tool externally and sends the tool result back, then the server resumes and completes the answer.

If the LLM does not call any tool, call 1 finishes normally and call 2 is not needed.

The graph node order is:

- `call_tool_llm` (llm).
- `external_tool` (tool, interrupt + ingest tool result).
- `answer_llm` (llm).

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.
go run ./server/externaltool \
  -model deepseek-chat \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Verify

Call 1 does not require `forwardedProps.lineage_id`. When a tool call happens, call 1 will include the generated `lineageId` in a `graph.node.interrupt` activity event. Call 2 must send this value back via `forwardedProps.lineage_id` to resume from the checkpoint.

### Option 1: `run.sh` (curl)

Run the helper script. It performs both requests (when a tool call happens), auto-captures `toolCallId` and `lineageId`, and writes the raw SSE outputs to:

- `run1.log` (call 1).
- `run2.log` (call 2).

From the repo root:

```bash
bash ./examples/agui/server/externaltool/run.sh
```

### Option 2: TDesign chat client

Use the [TDesign chat client](../../client/tdesign-chat) for an interactive UI.

### Request / response notes

- Call 1 request: `messages[-1]` is `{role:"user", content:"..."}` (string).
- Call 1 response (when tool called): streams `TOOL_CALL_START` / `TOOL_CALL_ARGS` / `TOOL_CALL_END`, then emits `ACTIVITY_DELTA(activityType="graph.node.interrupt")` (includes `lineageId`) and ends with `RUN_FINISHED` after the graph interrupts at `external_tool`.
- Call 2 request: `forwardedProps.lineage_id` must be the `lineageId` from call 1, and `messages[-1]` is `{role:"tool", name:"external_search", toolCallId:"<from call 1>", content:"..."}` (string).
- Call 2 response: emits `TOOL_CALL_RESULT` immediately after `RUN_STARTED`, then streams the resumed answer and ends with `RUN_FINISHED`.
- `toolCallId` must match the ID emitted in call 1, otherwise the server rejects the tool result.
- `messages[0].id` (call 2) is used as the `messageId` for `TOOL_CALL_RESULT` (defaults to `toolCallId` if empty).
- The checkpoint saver is in-memory; do not restart the server between call 1 and call 2.
