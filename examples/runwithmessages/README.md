# RunWithMessages: Seed Once, Then Latest Only

This example shows how to drive an Agent with a caller‑supplied conversation
history. We construct a multi‑turn history (system + user/assistant) and pass it
only on the first user turn of a session (and after reset). For all subsequent
turns, we only send the latest user message; Runner appends it to the session
and the agent reads full context from session events.

## Highlights

- **Auto session priming** – Runner converts your history into session events on
  first use (when the session is empty).
- **Seamless continuation** – Subsequent turns continue to append to the
  session automatically.
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

go run main.go -model deepseek-chat -streaming=true
```

Chat commands:

- `/reset` — start a brand-new session and reseed the default dialogue
- `/exit` — quit the demo

Try asking things like:

- "Please add 12.5 and 3" → the agent should call the `calculate` tool.
- "Compute 15 divided by 0" → should return an error from the tool.
- "What is 2 power 10?" → uses `calculate` with operation `power`.

## How it works

- Prepare a multi‑turn `[]model.Message` (system + user/assistant few turns).
- On the first user input, call `RunWithMessages(...)` with `history + latest user`.
- Afterwards, call `r.Run(...)` with only the latest user message; Runner will
  append to the session and the content processor will read the entire context
  from session events.

## Relation to `agent.WithMessages`

- Passing `agent.WithMessages` (or `runner.RunWithMessages`) persists the
  supplied history to the session on first use. The content processor does not
  read this option; it only converts session events (and falls back to a single
  `invocation.Message` when the session has no events).

Notes:

- When `[]model.Message` is provided, the content processor prioritizes these messages and skips deriving content from session events or the single `message` to avoid duplication.
- `RunWithMessages` sets `invocation.Message` to the latest user message for compatibility with graph/flow agents that use initial user input.
- Runner still persists events to its session service by default, but this session is not used to build the LLM request when messages are explicitly supplied.

## Compare with examples/runner

- `examples/runner` demonstrates multi-turn chat using Runner with server-side session state.
- `examples/runwithmessages` shows a stateless approach where you control the full prompt per run — a good fit for building middleware services where the upstream system already maintains the session.

## Customize

- Change the initial system message to guide behavior.
- Toggle `-streaming=false` to get full responses in one piece.
- Replace the model via `-model` (e.g., `gpt-4o-mini`, `deepseek-chat`).

---

For more details, see docs:

- English: `docs/mkdocs/en/runner.md` → “Pass Conversation History (no session dependency)”
- 中文: `docs/mkdocs/zh/runner.md` → “传入对话历史（无需使用 Session）”
