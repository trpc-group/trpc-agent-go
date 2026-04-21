# Stream Tool AG-UI Server

This example demonstrates a minimal real `StreamableTool` wired into AG-UI with the built-in streamed tool-result activity option.

The tool simply counts upward and streams text progress updates:

- partial tool output becomes `ACTIVITY_SNAPSHOT` and `ACTIVITY_DELTA`
- the final tool output remains a standard `TOOL_CALL_RESULT`

This keeps the example focused on the event model instead of tool-specific business logic.

## What This Example Shows

- A real `StreamableTool` built with `function.NewStreamableFunctionTool`.
- `agui.WithStreamingToolResultActivityEnabled(true)` rewriting partial `tool.response` into activity updates.
- A final `TOOL_CALL_RESULT` that still carries the structured tool result for the next LLM turn and for history replay.
- `MessagesSnapshot` enabled, while `/history` only keeps the final tool result.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.

go run ./server/streamtool \
  -model deepseek-chat \
  -address 127.0.0.1:8080 \
  -path /agui
```

The server exposes:

- chat endpoint: `http://127.0.0.1:8080/agui`
- history endpoint: `http://127.0.0.1:8080/history`

## Try It

Prompts that work well with this example:

- `Count to 5 with 200 millisecond updates.`
- `Run the counting tool for 8 steps.`
- `Count to 3 slowly, then tell me the final result.`

The agent is instructed to call `count_progress` exactly once for every user request before answering.

## Expected Event Shape

When the model calls the tool, the stream looks like:

```text
RUN_STARTED
TOOL_CALL_START
TOOL_CALL_ARGS
TOOL_CALL_END
ACTIVITY_SNAPSHOT(activityType="tool.result.stream")
ACTIVITY_DELTA(activityType="tool.result.stream")
ACTIVITY_DELTA(activityType="tool.result.stream")
...
TOOL_CALL_RESULT
TEXT_MESSAGE_*
RUN_FINISHED
```

This is the key behavior demonstrated by the example:

- intermediate tool execution is rendered as activity updates
- the final tool output remains a standard `TOOL_CALL_RESULT`
- `/history` keeps only the final tool message for the tool call

## Inspect the Raw SSE Stream

You can inspect the raw protocol stream with `curl`:

```bash
curl -N http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "streamtool-demo",
    "runId": "streamtool-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Count to 5 with 200 millisecond updates."
      }
    ]
  }'
```

Look for:

- `TOOL_CALL_*` frames
- `ACTIVITY_SNAPSHOT` / `ACTIVITY_DELTA` with `activityType: "tool.result.stream"`
- a single final `TOOL_CALL_RESULT`

## Notes

- The counting tool is intentionally small so the event flow is easy to inspect.
- The streamed activity content is cumulative text progress, so the raw SSE output is easy to read without a custom translator.
- The final tool result is still returned through the normal tool-response path, so the assistant can summarize the completed step count in its final answer.
