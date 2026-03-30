# User Steer Example

This example demonstrates how to insert a new `role=user` message into the
same run while the agent is still working.

It uses the `runner.EnqueueUserMessage(...)` API.

The easiest mental model is:

- One assistant message is one round
- If that assistant message starts tool calls, the round is not done until
  all tool results for that message are finished
- The queued user message is inserted only between two rounds

In message form, the valid shape is:

```text
user(Q1)
assistant(tool_call)
tool(tool_response)
user(Q2, queued steer)
assistant(...)
```

If one assistant message emits multiple tool calls, the framework still waits
for the whole round:

```text
user(Q1)
assistant(tool_calls A, B)
tool(result A)
tool(result B)
user(Q2, queued steer)
assistant(...)
```

It does not insert in the middle of that tool round.

## Why this example exists

The common real-world case is:

1. A user asks a question
2. The model starts a tool call
3. Before the run finishes, the user sends an extra instruction
4. You want the same run to continue with that new instruction

Starting a second concurrent run for the same session can create ordering and
state problems. This example shows the simpler approach: queue the new user
message into the current run and let the framework insert it at the next round
boundary.

## What the demo does

- The initial user message asks for a short launch announcement
- The model must call `load_launch_brief`
- The tool waits for a short time to simulate a slow upstream service
- While the tool is still running, the demo queues another user message
- The run continues and the final answer reflects both the tool result and the
  queued instruction

## Prerequisites

- Go 1.24 or later for the `examples/` module
- `OPENAI_API_KEY`
- Optional `OPENAI_BASE_URL` for OpenAI-compatible endpoints

## Run It

```bash
cd examples/steer
export OPENAI_API_KEY="your-api-key"
go run .
```

If you want to use a different model:

```bash
go run . -model gpt-4.1-mini
```

If you want a wider timing window:

```bash
go run . -tool-delay=3s -steer-after=1500ms
```

## Expected Output Shape

You should see output similar to:

```text
[model] tool_call load_launch_brief args={"project":"Project Atlas"}
[steer] queued extra user message at 1s
[tool] result: {"project":"Project Atlas", ...}
[queue] persisted queued user message: Update the draft: ...
[assistant] ...
[run] runner completion
```

The important detail is the order:

- The extra user message is queued while the tool is still running
- The queued message is persisted only after that assistant round is complete
- The final assistant answer uses the queued instruction

## Production Note

This demo uses a timer to simulate “another user message arrives while the tool
is running”.

In production, the enqueue usually happens from another goroutine, request
handler, websocket callback, or stream consumer that already knows the active
`requestID`.
