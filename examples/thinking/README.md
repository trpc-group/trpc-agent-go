# Thinking Demo (Reasoning)

This example demonstrates a clean chat interface that surfaces the model's internal reasoning (shown as dim text) alongside the final answer. It uses the Runner with streaming output and in-memory session management.

## What is Shown

- 🧠 Reasoning (Thinking): Appears as dim text in the terminal
- 🌊 Streaming vs Non-streaming: Real-time deltas vs one-shot response
- 💾 Session History: View previous turns (reasoning included when present)
- 🎛️ Simple Flags: `-model`, `-streaming`, `-thinking`, `-thinking-tokens`

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument            | Description                                  | Default Value        |
| ------------------- | -------------------------------------------- | -------------------- |
| `-model`            | Name of the model to use                     | `deepseek-reasoner`  |
| `-streaming`        | Enable streaming mode for responses          | `true`               |
| `-thinking`         | Enable reasoning/thinking (if provider supports) | `true`            |
| `-thinking-tokens`  | Max reasoning tokens (if provider supports)  | `2048`               |

## Usage

### Basic Run

```bash
cd examples/thinking
export OPENAI_API_KEY="your-api-key"
go run .
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model deepseek-chat
```

### Response Modes

```bash
# Default: streaming mode (real-time deltas)
go run .

# Non-streaming: complete response at once
go run . -streaming=false
```

When to use each mode:

- Streaming (`-streaming=true`, default): Best for interactive UX; shows dim reasoning as it streams, then the final answer.
- Non-streaming (`-streaming=false`): Prints reasoning (if provided in final message) once, followed by the answer.

### Help and Available Options

```bash
go run . --help
```

Example output:

```
Usage of ./thinking:
  -model string
        Name of the model to use (default "deepseek-reasoner")
  -streaming
        Enable streaming mode for responses (default true)
  -thinking
        Enable reasoning/thinking mode if provider supports it (default true)
  -thinking-tokens int
        Max reasoning tokens if provider supports it (default 2048)
```

## Chat Interface

You will see a simple interface similar to the Runner demo:

```
🧠 Thinking Demo (Reasoning)
Model: deepseek-reasoner
Streaming: true
Thinking: true (tokens=2048)
==================================================
✅ Ready! Session: thinking-session-1703123456
(Note: dim text indicates internal reasoning; normal text is the final answer)

💡 Special commands:
   /history  - Show conversation history
   /new      - Start a new session
   /exit     - End the conversation

👤 You: What is LLM
🤖 Assistant:  [dim reasoning streaming here...]
                [then the final visible answer...]
```

### Session Commands

- `/history` - Show conversation history (timestamps, role, reasoning if present)
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation

## Reasoning Display Details

- Streaming mode:
  - Dim reasoning is printed as deltas arrive.
  - A blank line is inserted before printing the normal answer content for readability.
  - The framework aggregates streamed reasoning into the final message so `/history` can display it.
- Non-streaming mode:
  - The final response may include reasoning; it is printed once in dim style before the answer.

## Notes

- This demo uses the in-memory session service for simplicity.
- Reasoning visibility depends on the provider/model. Enabling flags signals intent but does not guarantee reasoning will be returned.
