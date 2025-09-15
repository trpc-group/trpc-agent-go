# RunWithMessages: Pass Caller-Supplied Conversation History

This example demonstrates how to drive an Agent with a full `[]model.Message` conversation history provided by the caller, rather than relying on the Runner’s session contents. It uses a simple interactive CLI and streams assistant responses.

## What this shows

- **Pass history directly**: Provide `[]model.Message` to the Agent for each run.
- **No session dependency**: Upstream service can own and maintain the chat history.
- **Interactive + streaming**: Real-time token streaming in a terminal chat loop.
- **Backwards compatible**: Uses `runner.RunWithMessages` or `agent.WithMessages` without changing Runner API.

## Prerequisites

- Go 1.23+ (examples/go.mod uses 1.24 toolchain)
- Valid API key (OpenAI-compatible)

Environment variables:

- `OPENAI_API_KEY` (required)
- `OPENAI_BASE_URL` (optional, defaults to OpenAI)

## Run

```bash
cd examples/runwithmessages
export OPENAI_API_KEY="your-api-key"
# Optional: export OPENAI_BASE_URL="https://api.openai.com/v1" or another compatible endpoint

go run main.go -model deepseek-chat -streaming=true
```

Commands in the chat:

- `/reset` — clear local history and start fresh
- `/exit` — quit

## Core idea (two options)

Option A — convenience helper:

```go
// Maintain local history as []model.Message
history := []model.Message{
    model.NewSystemMessage("You are a helpful assistant."),
    model.NewUserMessage("Hello"),
    model.NewAssistantMessage("Hi there!"),
    model.NewUserMessage("What’s the weather?"),
}

ch, err := runner.RunWithMessages(ctx, r, userID, sessionID, history)
```

Option B — explicit RunOption:

```go
ch, err := r.Run(ctx, userID, sessionID, model.Message{}, agent.WithMessages(history))
```

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

