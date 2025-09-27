# Token Tailoring Example

This example demonstrates interactive token tailoring using `openai.WithTokenLimit` (plus optional overrides) and the built-in strategies in `model/token_tailor.go`.

## What it shows

- Configure a model with token tailoring via option.
- Use a simple counter (or swap to the `tiktoken` submodule) to enforce a prompt budget.
- Interactively send messages; use `/bulk N` to append many user messages and observe trimming.
- Demonstrates optimized O(n) time complexity token tailoring with prefix sum and binary search.

## Prerequisites

- Go 1.23 or later
- Optional: An OpenAI-compatible API key for real calls (you can still see tailoring stats without it).

## Run

```bash
cd examples/tailor

# Basic run with flags (defaults shown):
go run . \
  -model deepseek-chat \
  -token-limit 512 \
  -counter tiktoken \  # or: simple
  -strategy middle \
  -streaming=true
```

Then type:

```
/bulk 10
What is LLM
```

You should see lines like (banner + tailoring stats + message summary):

```
✂️  Token Tailoring Demo
🧩 model: deepseek-chat
🔢 token-limit: 512
🧮 counter: tiktoken
🎛️ strategy: middle
📡 streaming: true
==================================================
💡 Commands:
  /bulk N     - append N synthetic user messages
  /history    - show current message count
  /exit       - quit

👤 You: /bulk 10
Added 10 messages. Total=11
👤 You: What is LLM
🤖 Assistant: An LLM (Large Language Model) is a type of artificial intelligence model designed to understand, generate, and work with human language...

[tailor] tokenLimit=512 before=12 after=7
[tailor] messages (after tailoring):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum ...
[2] user: synthetic 2: lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum ...
[3] user: synthetic 8: lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum ...
[4] user: synthetic 9: lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum ...
[5] user: synthetic 10: lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum lorem ipsum...
[6] user: What is LLM
👤 You: /exit
👋 Goodbye!
```

The output shows:

- Interactive conversation with the AI assistant
- Token tailoring statistics: `[tailor] tokenLimit=512 before=12 after=7`
- The tailored messages that were sent to the model (index, role, truncated content)
- Different strategies produce different message selections (middle strategy preserves head and tail messages)

## Important Configuration Notes

✅ **Automatic Preservation**: The system automatically preserves important messages:

- **System message**: Always preserved to maintain AI role and behavior
- **Last turn**: Always preserved to maintain conversation continuity

This ensures optimal user experience without manual configuration.

## Notes

- You can switch to the `tiktoken` submodule counter for higher accuracy without changing the root Go version.
- Use `-streaming=true|false` to control streamed vs non-streamed responses. In streaming mode, tokens are printed incrementally; in non-streaming, the final answer is printed once.
- Available strategies:
  - `middle` (MiddleOutStrategy): Removes from the middle first, preserves head and tail messages
  - `head` (HeadOutStrategy): Removes from the head first, preserves tail messages
  - `tail` (TailOutStrategy): Removes from the tail first, preserves head messages

**Strategy Behavior Examples:**

- **Middle strategy**: `[0] system, [1] synthetic 1, [2] synthetic 2, [3] synthetic 8, [4] synthetic 9, [5] synthetic 10, [6] What is LLM`
- **Head strategy**: `[0] system, [1] synthetic 6, [2] synthetic 7, [3] synthetic 8, [4] synthetic 9, [5] synthetic 10, [6] What is LLM`
- **Tail strategy**: `[0] system, [1] synthetic 1, [2] synthetic 2, [3] synthetic 3, [4] synthetic 4, [5] synthetic 10, [6] What is LLM`

### Switch to tiktoken counter (optional)

In your code, replace:

```go
counter := model.NewSimpleTokenCounter(tokenLimit)
```

with a `tiktoken` submodule counter (see `model/tiktoken`).

Minimal setup requires only the token limit:

```go
m := openai.New("model-name",
    openai.WithTokenLimit(512),
)
```

Optionally override counter and/or strategy (if omitted, they default to SimpleTokenCounter and MiddleOutStrategy respectively):

```go
m := openai.New("model-name",
    openai.WithTokenLimit(512),
    openai.WithTokenCounter(tkCounter),              // optional
    openai.WithTailoringStrategy(customStrategy),    // optional
)
```

## Commands

- `/bulk N`: Append N user messages at once (defaults to 10 when N is omitted).
- `/history`: Show the current number of buffered messages.
- `/exit`: Quit.
