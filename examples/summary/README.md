# 📝 Session Summarization Example

This example demonstrates LLM-driven session summarization integrated with the framework's `Runner` and `session.Service`.

- Preserves original `events`.
- Stores summary separately from events (not inserted as system events).
- Feeds LLM with "latest summary + incremental event window" to control context size.
- Uses `SummarizerManager` as an optional in-process cache for fast reads.

## What it shows

- LLM-based summarization per session turn.
- Simple trigger configuration using event-count threshold.
- Prompt construction that injects the latest summary and recent events.
- Backend-specific persistence:
  - In-memory service mirrors summary text to `sess.State`.
  - Redis service mirrors summary text to Redis `SessionState.State` (see service code).

## Prerequisites

- Go 1.23 or later.
- Model configuration (e.g., OpenAI-compatible) via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/summary
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o-mini -window 25
```

Command-line flags:

- `-model`: Model name to use for both chat and summarization. Default: `deepseek-chat`.
- `-window`: Event window size for summarization input. Default: `50`.

## Interaction

- Type any message and press Enter to send.
- Type `/exit` to quit the demo.
- After each assistant reply, the example triggers summarization and prints the latest summary.

Example output:

```
📚 Session Summarization Demo
Model: gpt-4o-mini
Service: inmemory
Window: 25
SessionID: summary-session-1736480000

👤 You: Hello there!
🤖 Assistant: Hi! How can I help you today?
📝 Summary:
- The user greeted the assistant...
```

## Architecture

```
User → Runner → Agent(Model) → Session Service → (SummarizerManager + Summarizer)
                                    ↑
                            Persist summary text
```

- The `Runner` orchestrates the conversation and writes events.
- The `session.Service` triggers summarization after each turn via `CreateSessionSummary`.
- The `SummarizerManager` optionally caches the latest summary in memory.
- The `session.Service` mirrors summary text to its backend storage (in-memory or Redis).

## Key design choices

- Do not modify or truncate original `events`.
- Do not insert summary as an event. Summary is stored separately.
- `window` controls the LLM input size only.
- Default trigger uses an event-count threshold aligned with Python (`>` semantics).

## Files

- `main.go`: Interactive chat that triggers summarization and prints the latest summary text.

## Notes

- If the summarizer or manager is not configured, the service logs a warning and skips summarization.
- No fallback summaries by string concatenation are provided. The system relies on the configured LLM.
