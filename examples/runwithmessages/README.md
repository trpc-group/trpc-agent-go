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

- Go 1.23+ (examples/go.mod targets 1.24 toolchain)
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

## Next Steps

- Inspect `examples/runwithmessages/main.go` to see the “seed once, then latest” flow.
- Compare with `examples/runner`, which builds conversation state solely via the runner.
- Read more in `docs/mkdocs/en/runner.md` (English) or `docs/mkdocs/zh/runner.md`.
