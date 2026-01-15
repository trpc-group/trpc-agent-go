# External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` that demonstrates an external tool workflow.

The workflow is intentionally split into two requests:

- Call 1 (`role=user`): the LLM emits `TOOL_CALL_*` events, then the graph interrupts (checkpoints) at the tool node.
- Call 2 (`role=tool`): the caller executes the tool externally and sends the tool result back, then the server resumes and completes the answer.

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

## Verify With curl

This example requires `forwardedProps.lineage_id` so the server can resume the latest checkpoint within the same lineage.

### Run end-to-end

Run the helper script. It performs both requests, auto-captures `toolCallId`, and writes the raw SSE outputs to:

- `run1.log` (call 1).
- `run2.log` (call 2).

From the repo root:

```bash
bash ./examples/agui/server/externaltool/run.sh
```

Alternatively, use the Go client example:

```bash
cd examples/agui
go run ./client/externaltool -question "What is trpc-agent-go?"
```

Notes:

- `toolCallId` must match the ID emitted in call 1, otherwise the server rejects the tool result.
- `messages[0].id` (call 2) is used as the `messageId` for `TOOL_CALL_RESULT` (defaults to `toolCallId` if empty).
- `content` must be a string.
- Call 2 emits `TOOL_CALL_RESULT` immediately after `RUN_STARTED`.
- The checkpoint saver is in-memory; do not restart the server between call 1 and call 2.
