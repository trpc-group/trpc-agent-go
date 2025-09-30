# Thinking Demo (Reasoning)

This example focuses on demonstrating models' reasoning output ("thinking") and how it is streamed (dim text) and persisted in sessions.

## Features

- **🧠 Reasoning display**: Internal reasoning is shown in dim text (ANSI), final answer in normal text
- **🌊 Streaming/Non-streaming**: Toggle with a flag
- **💾 In-memory sessions**: Persist chat and reasoning-only events
- **🎛️ Minimal flags**: `-model`, `-streaming`, `-thinking`, `-thinking-tokens`

## Prerequisites

- Go 1.21+
- Valid OpenAI-compatible API credentials

Environment variables:

| Variable          | Description                              | Default                     |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) |                             |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Usage

```bash
cd examples/thinking
export OPENAI_API_KEY="your-api-key"
# Default: streaming on, thinking on
go run . -model deepseek-reasoner

# Non-streaming
go run . -model deepseek-reasoner -streaming=false

# Control thinking and tokens
go run . -model deepseek-reasoner -thinking=true -thinking-tokens=2048
```

You should see output like:

```
🧠 Thinking Demo (Reasoning)
Model: deepseek-reasoner
Streaming: true
Thinking: true (tokens=2048)
==================================================
✅ Ready! Session: thinking-session-...
(Note: dim text indicates internal reasoning; normal text is the final answer)

👤 You: explain quicksort briefly
🤖 Assistant:
<dim>I will outline the steps first... then provide a short answer...</dim>

QuickSort is a divide-and-conquer algorithm that...
```

## Notes

- Reasoning-only assistant events are persisted and will be retrievable via session `GetSession` once a user message exists in the history (the example always starts with a user input).
- This demo intentionally uses the in-memory session service for simplicity.
