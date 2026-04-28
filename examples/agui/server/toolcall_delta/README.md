# Tool Call Delta AG-UI Server

This example demonstrates streaming tool-call arguments through AG-UI.

The agent is instructed to generate a document and call:

```text
create_document(title, content)
```

The expensive part is generating the `content` argument. With both streaming switches enabled, AG-UI clients can see the `content` argument arrive incrementally as `TOOL_CALL_ARGS` events before the tool starts running.

## What This Example Shows

- `openai.WithShowToolCallDelta(true)` forwards streaming provider `tool_calls` deltas.
- `agui.WithToolCallDeltaStreamingEnabled(true)` emits those deltas as AG-UI `TOOL_CALL_ARGS`.
- The final `Message.ToolCalls` snapshot is still used to close the tool call without duplicating it.
- `create_document` saves the generated content in memory and returns a document id.

## Run

From the `examples/agui` module:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"

go run ./server/toolcall_delta \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

The server exposes:

- chat endpoint: `http://127.0.0.1:8080/agui`
- history endpoint: `http://127.0.0.1:8080/history`

## Try It

Use the raw AG-UI client from another terminal:

```bash
go run ./client/raw -endpoint http://127.0.0.1:8080/agui
```

Prompts that work well:

- `Generate a one-page onboarding guide for a new engineer and save it as a document.`
- `Create a Chinese product requirements document for tool-call argument streaming.`
- `Write a short incident review template and save it.`

## Inspect the Raw SSE Stream

```bash
curl -N http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "toolcall-delta-demo",
    "runId": "toolcall-delta-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Create a short Chinese product requirements document for AG-UI tool-call argument streaming and save it."
      }
    ]
  }'
```

Look for this event shape:

```text
RUN_STARTED
TOOL_CALL_START
TOOL_CALL_ARGS
TOOL_CALL_ARGS
...
TOOL_CALL_END
TOOL_CALL_RESULT
TEXT_MESSAGE_*
RUN_FINISHED
```

The multiple `TOOL_CALL_ARGS` frames are the key part: the frontend can render the generated `content` argument while the model is still producing it.
