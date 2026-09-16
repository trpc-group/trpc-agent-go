# RunWithMessages + Noop: Complete History on Every Request

This example shows how to drive an Agent when the application owns the complete
conversation history. Runner uses `session/noop`, so the application passes the
updated history to `RunWithMessages` on every request instead of relying on a
server-side Session from an earlier request.

## Highlights

- **Caller-owned history** – The application retains user, assistant, tool-call,
  and tool-result messages.
- **No Session persistence** – `session/noop` creates only a transient Session
  for each run.
- **Complete context every time** – Every request uses `RunWithMessages` with the
  full updated history.
- **Streaming CLI** – Talk to the agent in a terminal, reset on demand.

## Prerequisites

- Go 1.21 or later
- Valid API key (OpenAI-compatible)

Environment variables:

- `OPENAI_API_KEY` (required)
- `OPENAI_BASE_URL` (optional, defaults to OpenAI)

## Run It

```bash
cd examples/runwithmessages
export OPENAI_API_KEY="your-api-key"
# Optional: export OPENAI_BASE_URL="https://api.openai.com/v1" (or another endpoint)

go run main.go -model deepseek-v4-flash -streaming=true
```

Chat commands:

- `/reset` — start a brand-new session and reseed the default dialogue
- `/exit` — quit the demo

Try asking things like:

- "Please add 12.5 and 3" → the agent should call the `calculate` tool.
- "Compute 15 divided by 0" → should return an error from the tool.
- "What is 2 power 10?" → uses `calculate` with operation `power`.

## How it works

- Inject `session/noop` through `runner.WithSessionService`.
- Maintain a complete `[]model.Message` transcript in the application.
- Append each user message before the run.
- Call `RunWithMessages(...)` with the complete updated history on every request.
- Consume the event stream and append complete assistant tool calls, tool
  results, and assistant replies for the next request.

## Relation to `agent.WithMessages`

- Passing `agent.WithMessages` (or `runner.RunWithMessages`) seeds the transient
  Session used by the current run. With `session/noop`, that Session is discarded
  after the run, so the next request must pass the complete history again.

Notes:

- When `[]model.Message` is provided, the content processor prioritizes these messages and skips deriving content from session events or the single `message` to avoid duplication.
- `RunWithMessages` sets `invocation.Message` to the latest user message for compatibility with graph/flow agents that use initial user input.
- The example copies complete, non-partial response messages from the event
  stream into its caller-owned transcript.

## Compare with examples/runner

- `examples/runner` demonstrates multi-turn chat using Runner with server-side session state.
- `examples/runwithmessages` uses Noop and caller-owned history — a good fit for
  middleware where the upstream system already maintains the conversation.

## Customize

- Change the initial system message to guide behavior.
- Toggle `-streaming=false` to get full responses in one piece.
- Replace the model via `-model` (e.g., `gpt-4o-mini`, `deepseek-v4-flash`).

---

For more details, see docs:

- English: `docs/mkdocs/en/session/noop.md` → “No Persistence (Noop)”
- 中文: `docs/mkdocs/zh/session/noop.md` → “无持久化（Noop）”
