# ✂️ Token Tailoring Example

This example demonstrates interactive token tailoring with dual-mode configuration (automatic and advanced) using the built-in strategies in `model/token_tailor.go`.

- Automatic context window detection based on model configuration.
- Intelligent message preservation (system message + last conversation turn).
- Three tailoring strategies: middle-out, head-out, and tail-out.
- Real-time token statistics display with emoji-enhanced output.
- Optimized O(n) time complexity with prefix sum and binary search.

## What it shows

- Configure a model with token tailoring via option.
- Use a simple counter (or swap to the `tiktoken` submodule) to enforce a prompt budget.
- Interactively send messages; use `/bulk N` to append many user messages and observe trimming.
- Display token consumption statistics before and after tailoring.
- Visualize which messages are kept/removed with head+tail display format.

## Prerequisites

- Go 1.23 or later.
- Optional: An OpenAI-compatible API key for real calls (you can still see tailoring stats without it).

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/tailor
export OPENAI_API_KEY="your-api-key"  # Optional: for real API calls
```

**Simple mode (recommended)**: automatic token management

```bash
go run . -model deepseek-chat -enable-token-tailoring
```

**Advanced mode**: custom parameters

```bash
go run . \
  -model deepseek-chat \
  -enable-token-tailoring \
  -max-input-tokens 10000 \
  -strategy middle \
  -streaming=true
```

**Testing without API**: see tailoring statistics without making real API calls

```bash
go run . -model deepseek-chat -enable-token-tailoring -streaming=false
```

Command-line flags:

- `-model`: Model name to use for chat. Default: `deepseek-chat`.
- `-enable-token-tailoring`: Enable automatic token tailoring. Default: `false`.
- `-max-input-tokens`: Max input tokens (0=auto from context window). Default: `0`.
- `-counter`: Token counter type: `simple` or `tiktoken`. Default: `simple`.
- `-strategy`: Tailoring strategy: `middle`, `head`, or `tail`. Default: `middle`.
- `-streaming`: Enable streaming mode for responses. Default: `true`.

## Interaction

- Type any message and press Enter to send.
- Type `/bulk N` to append N synthetic user messages at once.
- Type `/history` to show current message count in buffer.
- Type `/show` to display current messages (head + tail format).
- Type `/exit` to quit the demo.

Example output:

```
✂️  Token Tailoring Demo
🧩 model: deepseek-chat
🔧 enable-token-tailoring: true
🔢 max-input-tokens: auto (from context window)
🧮 counter: simple
🎛️ strategy: middle
📡 streaming: true
==================================================
💡 Commands:
  /bulk N     - append N synthetic user messages
  /history    - show current message count
  /show       - display current messages (head + tail)
  /exit       - quit

👤 You: /bulk 1100
✅ Added 1100 messages. Total=1101

👤 You: /show
📋 Current messages (total: 1101):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum lorem ipsum...
[2] user: synthetic 2: lorem ipsum lorem ipsum...
...
[9] user: synthetic 9: lorem ipsum lorem ipsum...
... (981 messages omitted)
[991] user: synthetic 991: lorem ipsum lorem ipsum...
...
[1100] user: synthetic 1100: lorem ipsum lorem ipsum...

👤 You: What is token tailoring?
🤖 Assistant: Token tailoring is a technique to manage message length to fit within a model's context window...

✂️  [Tailoring] maxInputTokens=auto 📨 messages=1102→1032 🎯 tokens=135414→126804
📝 Messages (after tailoring, showing head + tail):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum lorem ipsum...
[2] user: synthetic 2: lorem ipsum lorem ipsum...
[3] user: synthetic 3: lorem ipsum lorem ipsum...
[4] user: synthetic 4: lorem ipsum lorem ipsum...
... (1022 messages omitted)
[1027] user: synthetic 1097: lorem ipsum lorem ipsum...
[1028] user: synthetic 1098: lorem ipsum lorem ipsum...
[1029] user: synthetic 1099: lorem ipsum lorem ipsum...
[1030] user: synthetic 1100: lorem ipsum lorem ipsum...
[1031] user: What is token tailoring?

👤 You: /exit
👋 Goodbye!
```

The output shows:

- Interactive conversation with the AI assistant.
- Token tailoring statistics with emoji indicators:
  - ✂️ Tailoring applied
  - 📨 Message count (before → after)
  - 🎯 Token count (before → after)
- The tailored messages that were sent to the model (index, role, truncated content).
- Head+tail display format clearly shows which messages are preserved and which are omitted.

## Configuration Modes

### Simple Mode (Automatic)

Enable token tailoring without specifying max-input-tokens:

```bash
go run . -model deepseek-chat -enable-token-tailoring
```

Behavior:

- Automatically detects context window from model configuration.
- Calculates optimal `maxInputTokens` by subtracting protocol overhead and output reserve.
- Uses default `SimpleTokenCounter` and `MiddleOutStrategy`.

For `deepseek-chat`:

- Context window: 128,000 tokens
- Max input tokens: 126,848 tokens (128,000 - 128 - 1,024)
- Tailoring threshold: ~1,030 synthetic messages

### Advanced Mode (Customizable)

Specify custom `max-input-tokens` for precise control:

```bash
go run . -model deepseek-chat -enable-token-tailoring -max-input-tokens 10000
```

Behavior:

- Uses the specified token limit.
- Allows custom counter and strategy selection.
- Useful for testing or strict budget requirements.

## Tailoring Strategies

The framework provides three built-in strategies for different use cases:

### MiddleOutStrategy (Default)

Removes messages from the middle while preserving head and tail:

```bash
go run . -strategy middle -max-input-tokens 10000
```

Example result (1000 messages → 83 messages):

```
✂️  [Tailoring] maxInputTokens=10000 📨 messages=1002→83 🎯 tokens=123010→9973
📝 Messages (after tailoring, showing head + tail):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum...
[2] user: synthetic 2: lorem ipsum...
[3] user: synthetic 3: lorem ipsum...
[4] user: synthetic 4: lorem ipsum...
... (73 messages omitted)
[78] user: synthetic 997: lorem ipsum...
[79] user: synthetic 998: lorem ipsum...
[80] user: synthetic 999: lorem ipsum...
[81] user: synthetic 1000: lorem ipsum...
[82] user: What is LLM
```

**Best for**: Maintaining context from both early and recent conversation history.

### HeadOutStrategy

Removes messages from the head (older messages first):

```bash
go run . -strategy head -max-input-tokens 10000
```

Example result (1000 messages → 83 messages):

```
✂️  [Tailoring] maxInputTokens=10000 📨 messages=1002→83 🎯 tokens=123010→9973
📝 Messages (after tailoring, showing head + tail):
[0] system: You are a helpful assistant.
[1] user: synthetic 920: lorem ipsum...
[2] user: synthetic 921: lorem ipsum...
... keeps recent messages from 920-1000 ...
[82] user: What is LLM
```

**Best for**: Prioritizing recent conversation context (chat applications).

### TailOutStrategy

Removes messages from the tail (newer messages first):

```bash
go run . -strategy tail -max-input-tokens 10000
```

Example result (1000 messages → 83 messages):

```
✂️  [Tailoring] maxInputTokens=10000 📨 messages=1002→83 🎯 tokens=123010→9972
📝 Messages (after tailoring, showing head + tail):
[0] system: You are a helpful assistant.
[1] user: synthetic 1: lorem ipsum...
[2] user: synthetic 2: lorem ipsum...
... keeps early messages from 1-81 ...
[82] user: What is LLM
```

**Best for**: Preserving initial context and instructions (RAG applications).

## Important Configuration Notes

✅ **Automatic Preservation**: The system automatically preserves important messages:

- **System message**: Always preserved to maintain AI role and behavior.
- **Last turn**: Always preserved to maintain conversation continuity (last complete user-assistant pair with any tool messages).

This ensures optimal user experience without manual configuration.

## Architecture

```
User Input → OpenAI Model → Token Tailoring → API Request
                                    ↓
                            Check token limit
                                    ↓
                      Apply strategy if needed
                                    ↓
                        Return tailored messages
```

- The `OpenAI Model` checks if token tailoring is enabled.
- If enabled and tokens exceed limit, applies the configured strategy.
- Strategies use prefix sum and binary search for O(n) efficiency.
- Original message list remains unchanged; tailored list is sent to API.

## Token Counter Options

### SimpleTokenCounter (Default)

Fast estimation based on character count:

```go
// Automatically used in simple mode
counter := model.NewSimpleTokenCounter()
```

**Pros**: Fast, no external dependencies, good enough for most cases.
**Cons**: Less accurate than tiktoken.

### Tiktoken Counter (Optional)

Accurate token counting using OpenAI's tokenizer:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/tiktoken"

// Create tiktoken counter
tkCounter, err := tiktoken.New("gpt-4o")
if err != nil {
    // fallback to simple counter
}

// Use with model
m := openai.New(modelName,
    openai.WithEnableTokenTailoring(true), // Required: enable token tailoring
    openai.WithTokenCounter(tkCounter),
)
```

To switch to tiktoken in this example, modify the `makeCounter` function in `main.go`.

**Pros**: Accurate token counting matching OpenAI's API.
**Cons**: Requires additional dependency, slightly slower.

## Commands

Interactive commands available during the session:

- **`/bulk N`**: Append N user messages at once (defaults to 10 when N is omitted). Useful for testing tailoring behavior with large message counts.
- **`/history`**: Show the current number of buffered messages. Quick way to check message count without detailed display.
- **`/show`**: Display current messages in buffer (head + tail format). Shows first 10 and last 10 messages with omitted count in the middle.
- **`/exit`**: Quit the demo session.

## Code Integration

Minimal setup requires only the enable flag:

```go
m := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true), // Required: enable token tailoring
)
```

This enables automatic mode with:

- Context window auto-detection
- Default SimpleTokenCounter
- Default MiddleOutStrategy

Optionally override components:

```go
m := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),             // Required: enable token tailoring
    openai.WithMaxInputTokens(10000),                  // Custom limit
    openai.WithTokenCounter(customCounter),            // Custom counter
    openai.WithTailoringStrategy(customStrategy),      // Custom strategy
)
```

**Using Provider Package**:

You can also use the `provider` package for unified model instantiation:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/provider"
)

// Simple mode with provider
m, err := provider.Model(
    "openai",
    "deepseek-chat",
    provider.WithEnableTokenTailoring(true),
)

// Advanced mode with custom token tailoring config
config := &model.TokenTailoringConfig{
    ProtocolOverheadTokens: 1024,
    ReserveOutputTokens:    4096,
    SafetyMarginRatio:      0.15,
}
m, err := provider.Model(
    "openai",
    "deepseek-chat",
    provider.WithEnableTokenTailoring(true),
    provider.WithTokenTailoringConfig(config),
)
```

This approach allows easy switching between different providers (OpenAI, Anthropic, Ollama) with the same configuration interface.

## Performance Notes

### Tailoring Thresholds (Examples)

Based on testing with different models:

| Model         | Context Window | Max Input Tokens | Threshold (synthetic msgs) |
| ------------- | -------------- | ---------------- | -------------------------- |
| deepseek-chat | 128,000        | 126,848          | ~1,030                     |
| gpt-4o-mini   | 128,000        | 126,848          | ~1,030                     |
| gpt-4         | 8,192          | 7,040            | ~57                        |

_Threshold is based on synthetic messages with ~123 tokens each._

### Time Complexity

- **Token Counting**: O(n) with single pass through messages
- **Prefix Sum**: O(n) to build prefix sum array
- **Binary Search**: O(log n) to find optimal split point
- **Result Building**: O(k) where k is result message count
- **Overall**: O(n) for tailoring operation

This ensures efficient processing even with thousands of messages.

### Memory Usage

- **Prefix Sum Array**: O(n) additional space
- **Result Messages**: O(k) where k < n
- **Original Messages**: Unchanged (not copied)

Memory overhead is minimal and proportional to message count.

## Key Design Choices

- Do not modify original message list (immutable input).
- Preserve system message and last conversation turn automatically.
- Use prefix sum for O(1) range queries after O(n) preprocessing.
- Use binary search for optimal split point finding in O(log n).
- Support custom counter and strategy for flexibility.
- Dual-mode design: simple for ease-of-use, advanced for control.

## Files

- `main.go`: Interactive chat with token tailoring demonstration and statistics display.

## Notes

- Synthetic messages use "lorem ipsum" placeholder text for testing.
- Each synthetic message is approximately 123 tokens.
- The `/show` command displays up to 10 head and 10 tail messages.
- Token statistics are calculated using the same counter that will be used for tailoring.
- If API key is not configured, you can still see tailoring statistics (API call will fail gracefully).
