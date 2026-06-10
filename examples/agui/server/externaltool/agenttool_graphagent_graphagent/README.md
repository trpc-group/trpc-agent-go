# AgentTool GraphAgent-to-GraphAgent AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a parent `GraphAgent`.

The parent graph runs one tool call in a `ToolsNode`: `review_graph_tool`, an `AgentTool` that wraps a child `GraphAgent`.

The child `GraphAgent` pauses by calling `graph.Interrupt(...)`. The parent graph checkpoints the `ToolsNode`, so the next AG-UI request can resume from the parent checkpoint and continue inside the child graph.

The parent graph shape is:

- `call_review_graph`: runs the model with `review_graph_tool` available.
- `execute_tools`: runs `review_graph_tool`.
- `final_answer`: turns the resumed tool result into an assistant response.

The child graph shape is:

- `call_review_decision`: runs the model with `request_review_decision`
  available.
- `review`: interrupts on `review_decision` and records the resumed tool result.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.
go run ./server/externaltool/agenttool_graphagent_graphagent \
  -model gpt-5.2 \
  -address 127.0.0.1:8080 \
  -path /agui
```

The example uses in-memory checkpoint storage, so keep the server process
running between the first and second request.

## Verify

Run the standard-library Python client from the repo root:

```bash
python3 ./examples/agui/server/externaltool/agenttool_graphagent_graphagent/client.py
```

The client performs two requests, extracts the parent `execute_tools` checkpoint from the first SSE stream, resumes with `state.resume_map.review_decision`, and verifies that the resumed run emits a final text response.

The client keeps a stable `threadId` by default and generates fresh `runId`
and `lineage_id` values for each execution, so it can be run repeatedly
against the same server process. Pass `--thread-id` when you want a different
conversation, or pass `--run-id-1`, `--run-id-2`, and `--lineage-id` when you
need fixed IDs for debugging.

The two AG-UI requests are:

- Call 1: send a `role=user` message with `state.lineage_id`.
- Call 2: send the same `state.lineage_id`, the parent `state.checkpoint_id`,
  and `state.resume_map.review_decision`.

The second response includes a `TOOL_CALL_RESULT` for `review_graph_tool`
showing the child graph resumed with the supplied review decision. It also
includes `TEXT_MESSAGE_CONTENT` from the parent `final_answer` node.

The output has this two-step shape:

```text
Call 1: waiting for AgentTool child graph interrupt.
threadId: agenttool-demo-thread
lineageId: agenttool-demo-lineage-...
checkpointId: ...
toolCallId: ...

Call 2: resuming child graph through parent checkpoint.

Final answer:
...

Verified: child graph resumed with review decision approved.
```

## Notes

- Use the parent interrupt where `nodeId` is `execute_tools` for `state.checkpoint_id`.
- The child graph checkpoint is resumed internally by `AgentTool`; AG-UI callers should only pass the parent checkpoint and the child interrupt key in `state.resume_map`.
- The checkpoint saver is in-memory, so keep the server process running between the first and second request.
