# Token Tailoring Example

This example demonstrates interactive token tailoring using `openai.WithTokenTailoring` and the built-in strategies in `model/token_tailor.go`.

## What it shows

- Configure a model with token tailoring via option.
- Use a simple counter (or swap to the `tiktoken` submodule) to enforce a prompt budget.
- Interactively send messages; use `/bulk N` to append many user messages and observe trimming.

## Prerequisites

- Go 1.23 or later
- Optional: An OpenAI-compatible API key for real calls (you can still see tailoring stats without it).

## Run

```bash
cd examples/tailor

# Basic run with flags (defaults shown):
go run . \
  -model deepseek-chat \
  -max-prompt-tokens 512 \
  -counter tiktoken \  # or: simple
  -strategy middle \
  -preserve-system=true \
  -preserve-last-turn=true \
  -streaming=true
```

Then type:

```
/bulk 30
Hello
```

You should see lines like (banner + tailoring stats + message summary):

```
✂️  Token Tailoring Demo
🧩 model: deepseek-chat
🔢 max-prompt-tokens: 512
🧮 counter: tiktoken
🎛️ strategy: middle
🛡️ preserve-system: true
🛡️ preserve-last-turn: true
📡 streaming: true
==================================================
💡 Commands:
  /bulk N     - append N synthetic user messages
  /history    - show current message count
  /exit       - quit

[tailor] maxPromptTokens=512 before=35 after=6
[tailor] messages (after tailoring):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum ...
...
[5] user: Hello
```

The first line indicates tailoring happened and how many messages remain. The second block lists the messages sent after tailoring (index, role, truncated content).

## Notes

- You can switch to the `tiktoken` submodule counter for higher accuracy without changing the root Go version.
- Use `-streaming=true|false` to control streamed vs non-streamed responses. In streaming mode, tokens are printed incrementally; in non-streaming, the final answer is printed once.
- Available strategies:
  - `MiddleOutStrategy` (remove from the middle first; implicitly preserves head and tail)
  - `HeadOutStrategy` (remove from the head; options to preserve system and last turn)
  - `TailOutStrategy` (remove from the tail; options to preserve system and last turn)

### Switch to tiktoken counter (optional)

In your code, replace:

```go
counter := model.NewSimpleTokenCounter(maxPromptTokens)
```

with a `tiktoken` submodule counter (see `model/tiktoken`), and keep the same `WithTokenTailoring` usage.

## Commands

- `/bulk N`: Append N user messages at once (defaults to 10 when N is omitted).
- `/history`: Show the current number of buffered messages.
- `/exit`: Quit.
