# External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint that demonstrates handing off tool execution to the frontend. The agent uses `llmagent` with an OpenAI-compatible model and a `change_background` tool that returns `StopReasonExternalTool`, prompting the UI to apply the change and respond with a `role=tool` message.

It pairs with the CopilotKit client in [`examples/agui/client/copilotkit`](../../client/copilotkit/), which listens for the tool events, updates the page background, and sends the tool result back to AG-UI.

## How It Works

1. **Tool discovery**: The assistant emits a `change_background` tool call. The server registers the tool but never mutates UI state directly.
2. **Server response**: The Go handler validates the requested hex color and returns `agent.NewStopError(..., agent.WithStopReason(agent.StopReasonExternalTool))`. The translator still emits `TOOL_CALL_START`, `TOOL_CALL_ARGS`, and `TOOL_CALL_END`, but avoids `RunError`.
3. **Frontend execution**: The CopilotKit client intercepts the tool arguments, applies the color in the browser, and posts a second request to AG-UI with a `role=tool` message containing the matching `tool_call_id`.
4. **Conversation resume**: The runner persists the tool response so the agent can acknowledge the change or continue the dialogue with full history.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY=sk-xxx
go run ./server/externaltool
```

The server prints startup logs similar to:

```
2025/11/06 12:00:00 AG-UI external tool demo listening on http://127.0.0.1:8080/agui
2025/11/06 12:00:00 Ensure OPENAI_API_KEY is exported before running if the selected model requires authentication.
```

## CopilotKit Front Web Page Display

![CopilotKit external tool demo]()
