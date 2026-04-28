# Heartbeat AG-UI Server

This example demonstrates SSE heartbeat keepalive frames for AG-UI.

It wires `agui.WithHeartbeatInterval` into a regular AG-UI server backed by a real `LLMAgent`, an OpenAI-compatible model, and a real `FunctionTool`. The agent is instructed to call `wait_before_answer` before answering. While that tool is waiting, the server keeps the SSE connection active by writing heartbeat comment frames.

## What This Example Shows

- `agui.WithHeartbeatInterval(d)` on the default SSE transport.
- SSE comment frames (`:\n\n`) emitted while the run is active and no AG-UI event is ready.
- A normal AG-UI event stream before and after the tool quiet period.
- A real model-driven tool call through `function.NewFunctionTool`.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url"

go run ./server/heartbeat \
  -model deepseek-v3.2 \
  -address 127.0.0.1:8080 \
  -path /agui \
  -heartbeat 1s \
  -wait 5s
```

The server exposes:

- chat endpoint: `http://127.0.0.1:8080/agui`

## Try It

Inspect the raw SSE stream with `curl`:

```bash
curl -N http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "heartbeat-demo",
    "runId": "heartbeat-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Wait before answering, then say hello."
      }
    ]
  }'
```

In the raw output, heartbeat frames appear between normal `data:` frames while the run is active. A stream that includes the waiting tool looks like:

```text
data: {"type":"RUN_STARTED",...}
...
data: {"type":"TOOL_CALL_START",...}
data: {"type":"TOOL_CALL_ARGS",...}
data: {"type":"TOOL_CALL_END",...}
:

:

data: {"type":"TOOL_CALL_RESULT",...}
...
data: {"type":"TEXT_MESSAGE_START",...}
data: {"type":"TEXT_MESSAGE_CONTENT",...}
data: {"type":"TEXT_MESSAGE_END",...}
data: {"type":"RUN_FINISHED",...}
```

The `:` lines are heartbeat frames from the SSE transport.

## Options

- `-heartbeat`: heartbeat interval. `0` disables heartbeat frames.
- `-wait`: quiet period inside the `wait_before_answer` tool.
