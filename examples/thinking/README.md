# Thinking Demo (Reasoning)

This example demonstrates a clean chat interface that surfaces the model's internal reasoning (shown as dim text) alongside the final answer. It uses the Runner with streaming output and in-memory session management, and includes tool calling support for testing DeepSeek 3.2's thinking + tool call feature.

## What is Shown

- 🧠 Reasoning (Thinking): Appears as dim text in the terminal.
- 🌊 Streaming vs Non-streaming: Real-time deltas vs one-shot response.
- 💾 Session History: View previous turns (reasoning included when present).
- 🛠️ Tool Calling: Calculator and time tools to test thinking + tool call scenarios.
- 🎛️ Reasoning Mode: Control how `reasoning_content` is handled in multi-turn conversations.
- 🐛 Debug Mode: Print messages sent to model API (enabled by default).

## Prerequisites

- Go 1.21 or later.
- Valid API key for your model provider (OpenAI, DeepSeek, Qwen, etc.).

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for OpenAI (required for OpenAI) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |
| `DEEPSEEK_API_KEY`| API key for DeepSeek (auto-used when `-variant=deepseek`) | ``         |
| `DASHSCOPE_API_KEY`| API key for Qwen (auto-used when `-variant=qwen`) | ``                  |

## Command Line Arguments

| Argument            | Description                                  | Default Value        |
| ------------------- | -------------------------------------------- | -------------------- |
| `-model`            | Name of the model to use                     | `deepseek-reasoner`  |
| `-variant`          | Model variant: `openai`, `deepseek`, `qwen`, `hunyuan` | `openai`     |
| `-streaming`        | Enable streaming mode for responses          | `true`               |
| `-thinking`         | Enable reasoning/thinking (if provider supports) | `true`            |
| `-thinking-tokens`  | Max reasoning tokens (if provider supports)  | `2048`               |
| `-reasoning-mode`   | How to handle `reasoning_content` in history | `discard_previous`   |
| `-debug`            | Print messages sent to model API             | `true`               |

### Reasoning Mode Values

| Value              | Description                                                  |
| ------------------ | ------------------------------------------------------------ |
| `discard_previous` | Discard `reasoning_content` from previous turns, keep current (default, recommended). |
| `keep_all`         | Keep all `reasoning_content` in history.                     |
| `discard_all`      | Discard all `reasoning_content` from history.                |

## Usage

### Basic Run (DeepSeek)

```bash
cd examples/thinking
export DEEPSEEK_API_KEY="your-api-key"
go run . -model deepseek-chat -variant deepseek
```

### OpenAI Compatible

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-4o -variant openai
```

### Qwen QwQ

```bash
export DASHSCOPE_API_KEY="your-api-key"
go run . -model qwq-32b -variant qwen
```

### Keep All Reasoning (Optional)

If you need to keep all `reasoning_content` in history for debugging purposes:

```bash
go run . -model deepseek-chat -variant deepseek -reasoning-mode=keep_all
```

### Disable Debug Output

```bash
go run . -model deepseek-chat -variant deepseek -debug=false
```

### Response Modes

```bash
# Default: streaming mode (real-time deltas)
go run . -model deepseek-chat -variant deepseek

# Non-streaming: complete response at once
go run . -model deepseek-chat -variant deepseek -streaming=false
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
  -debug
        Print messages sent to model API for debugging (default true)
  -model string
        Name of the model to use (default "deepseek-reasoner")
  -reasoning-mode string
        How to handle reasoning_content in history: keep_all, discard_previous, discard_all (default "discard_previous")
  -streaming
        Enable streaming mode for responses (default true)
  -thinking
        Enable reasoning/thinking mode if provider supports it (default true)
  -thinking-tokens int
        Max reasoning tokens if provider supports it (default 2048)
  -variant string
        Name of Variant to use when use openai provider, openai / hunyuan / deepseek / qwen (default "openai")
```

## Chat Interface

You will see a simple interface similar to the Runner demo:

```
🧠 Thinking Demo (Reasoning)
Model: deepseek-chat
Streaming: true
Thinking: true (tokens=2048)
Reasoning Mode: discard_previous
Debug: true
==================================================
✅ Ready! Session: thinking-session-1703123456

💡 Special commands:
   /history  - Show conversation history
   /new      - Start a new session
   /exit     - End the conversation

👤 You: Calculate 123*456
🤖 Assistant:
────────────────────────────────────────────────────────────
📤 DEBUG: Messages sent to model API:
────────────────────────────────────────────────────────────
[0] SYSTEM: A focused demo showing reasoning content with optional tools...
[1] USER: Calculate 123*456
────────────────────────────────────────────────────────────
[dim reasoning streaming here...]

[tool call: calculator({"operation": "multiply", "a": 123, "b": 456})]

123 × 456 = 56088
```

### Available Tools

| Tool           | Description                                              |
| -------------- | -------------------------------------------------------- |
| `calculator`   | Perform basic mathematical calculations (add, subtract, multiply, divide). |
| `current_time` | Get the current time and date for a specific timezone.   |

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
- Debug mode is enabled by default to help verify `reasoning_content` handling in multi-turn conversations.
- The default `-reasoning-mode=discard_previous` follows DeepSeek API best practices: discard `reasoning_content` from previous turns while keeping the current turn's reasoning for tool call scenarios.
- The `calculator` and `current_time` tools are always available to test thinking + tool call scenarios.
