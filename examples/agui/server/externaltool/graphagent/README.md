# GraphAgent External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` that demonstrates a mixed internal/external tool workflow.

The workflow can be split into two requests:

- Call 1 (`role=user`): the graph first asks the LLM for `internal_lookup` and `internal_profile`, executes both internal tools, then asks the LLM for `external_search` and `external_approval`, and checkpoints when the external interrupt node waits for caller results.
- Call 2 (`role=tool`): the caller executes `external_search` and `external_approval` externally and sends both tool results back, then the graph resumes, records both external tool results, and generates the answer.

Call 2 is used after the event stream contains `external_search` and `external_approval` tool calls.

The graph shape is:

- `internal_call_llm` (llm, emits `internal_lookup` and `internal_profile` tool calls).
- `internal_tool` (tool, executes both internal tools with the graph built-in tools node).
- `external_call_llm` (llm, emits `external_search` and `external_approval` tool calls).
- `external_interrupt` (tool, interrupt + ingest `external_search` and `external_approval` results).
- `answer_llm` (llm).

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.
go run ./server/externaltool/graphagent \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Verify

Call 1 sends a `role=user` message. When a tool call happens, call 1 includes the generated `lineageId` and `checkpointId` in a `graph.node.interrupt` activity event. Call 2 sends these values back via `forwardedProps.lineage_id` and `forwardedProps.checkpoint_id` to resume from the checkpoint.

### Option 1: `run.sh` (curl)

Run the helper script. It performs both requests, auto-captures `toolCallId`, `lineageId`, and `checkpointId`, and writes the raw SSE outputs to:

- `${RUN_LOG_DIR}/run1.log` (call 1).
- `${RUN_LOG_DIR}/run2.log` (call 2).

`RUN_LOG_DIR` defaults to a temporary directory.

From the repo root:

```bash
bash ./examples/agui/server/externaltool/graphagent/run.sh
```

### Frontend reference

The [TDesign chat client](../../../client/tdesign-chat) is a frontend implementation reference. This GraphAgent example expects the caller to send one `role=tool` message for each pending external tool call in the same resume request; use `run.sh` above for the complete two-external-tool verification flow.

### Request / response notes

- Call 1 request: `messages[-1]` is `{role:"user", content:"..."}` (string).
- Call 1 response (when external tool execution is needed): streams internal `TOOL_CALL_*` events, emits `TOOL_CALL_RESULT` for `internal_lookup` and `internal_profile`, streams external `TOOL_CALL_*` events, then emits `ACTIVITY_DELTA(activityType="graph.node.interrupt")` (includes `lineageId` and `checkpointId`) and ends with `RUN_FINISHED` after the graph interrupts at `external_interrupt`.
- Call 2 request: `forwardedProps.lineage_id` and `forwardedProps.checkpoint_id` must come from call 1, and the tail of `messages` contains `role=tool` results for both `external_search` and `external_approval`.
- Call 2 response: emits `TOOL_CALL_RESULT` for `external_search` and `external_approval` immediately after `RUN_STARTED`, emits the graph interrupt resume acknowledgement, then streams the resumed answer and ends with `RUN_FINISHED`.
- Each `toolCallId` must match the corresponding ID emitted in call 1.
- Each tool message `id` (call 2) is used as the `messageId` for `TOOL_CALL_RESULT` (defaults to `toolCallId` if empty).
- The checkpoint saver is in-memory; keep the server process running between call 1 and call 2.
