# Session Summarizer Example

This example demonstrates a realistic multi-turn chat that integrates the session summarizer into the Runner. The summarizer automatically compresses older conversation events into a single system summary message after configurable conditions are met, while keeping the most recent events.

## Prerequisites

- Go 1.23 or later.
- Valid OpenAI API key (or compatible API endpoint).

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Usage

```bash
cd examples/summarizer
export OPENAI_API_KEY="your-api-key"
# Run with defaults (streaming enabled)
go run main.go

# Non-streaming mode
go run main.go -streaming=false

# Adjust turn threshold to summarize earlier/later
go run main.go -turns 2
```

## Commands

- `/summary` - Show current cached summary (if available).
- `/new` - Start a new session (resets conversation context).
- `/exit` - End the conversation.

## Behavior

- The Runner appends user and assistant events into the session as they complete.
- After the turn completes, Runner triggers summarization asynchronously via `SummarizerManager`.
- If checks pass, older events are compressed to a single system summary event, and recent events are kept.
- Summary text is stored only in process memory; external backends may skip persisting the synthetic summary event by design.
- Because summarization is asynchronous, if you immediately type `/summary` after a turn, you may need to send one more message or wait briefly to see the latest summary.

## Flags

- `-model` Model name to use (default: `deepseek-chat`).
- `-streaming` Enable streaming responses (default: `true`).
- `-turns` Approximate number of user plus assistant turns before summarization (default: `6`).

## Notes

- This example intentionally excludes tools to focus on summarization behavior.
- Default configuration keeps the most recent two events after summarization for easier demonstration.
- For Redis or other backends, the system summary event may not be persisted; the conversation is still compressed in memory for subsequent turns.
