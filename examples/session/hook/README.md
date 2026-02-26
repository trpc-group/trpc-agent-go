# Session Hooks Demo

This example shows how to use session hooks for:
1. **Content Filtering**: Mark and filter prohibited content via event tags.
2. **Consecutive User Messages**: Handle duplicate/consecutive user messages via `AppendEventHook` (alternative to `WithOnConsecutiveUserMessage`).

## What it demonstrates
- `AppendEventHook`: scans user/assistant messages, tags violations as `violation=<word>` (tags are joined by `event.TagDelimiter`, i.e. `;`).
- `GetSessionHook`: filters violated Q&A pairs out of session history before they reach the LLM context.
- `FixConsecutiveUserMessagesHook`: a `GetSessionHook` that fixes consecutive user messages at read time (simpler than using `AppendEventHook` because no persistence is needed).
- The console prints when a message is marked/filtered so you can see the hook chain in action.

## Prerequisites
- Go 1.21+
- LLM endpoint/key (OpenAI-compatible). Set:
  - `OPENAI_API_KEY`
  - `OPENAI_BASE_URL` (default `https://api.openai.com/v1`)
- Optional: `MODEL_NAME` (default `deepseek-chat`)

## Quick start
```bash
cd examples/session/hook
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

# Basic content filtering.
go run . -model="${MODEL_NAME:-deepseek-chat}"

# With consecutive user message handling (merge strategy).
go run . -consecutive=merge

# Other strategies: placeholder, skip.
go run . -consecutive=placeholder
go run . -consecutive=skip
```

## Consecutive User Message Strategies

The `-consecutive` flag enables handling of consecutive user messages via `GetSessionHook`:

| Strategy | Behavior |
|----------|----------|
| `merge` | Merge current message into previous user message (also re-checks for violations) |
| `placeholder` | Insert a placeholder assistant response in-memory between consecutive user messages (not persisted) |
| `skip` | Keep only the last user message in consecutive sequence |

This demonstrates that `GetSessionHook` is a simpler approach for fixing consecutive user messages compared to `AppendEventHook`, because:
1. No need to access `sessionService` (no persistence needed, just fix in-memory).
2. No recursion concerns.
3. Fixes happen at read time, keeping storage unchanged.

### When do consecutive user messages occur?

Consecutive user messages happen when a user message is written to the session but no assistant response follows. Common scenarios:

1. **User disconnection**: User sends a message, disconnects before receiving response, then reconnects and sends another message.
2. **Network issues**: Client retries due to timeout while the first request was already processed.
3. **Rapid messaging**: User sends multiple messages before the assistant can respond.

The demo simulates this by directly appending a user message to the session without waiting for an assistant response. When `-consecutive` is enabled, you'll see the hook handle this scenario.

## Expected flow (sample)
1) Normal message passes through, stored as-is.  
2) Message containing a prohibited phrase (e.g., "pirated serial number") is tagged `violation=pirated serial number`; on GetSession it and its paired reply are filtered.  
3) Subsequent requests continue; violated pairs stay out of context so the LLM does not see them.  

Console snippets you should notice:
- Marking: `Marked user message as violation (word: pirated serial number): ...`
- Filtering: `Filtered violation: ... tag=pirated serial number` and `Filtered paired response`

## Tag format
- Single tag per violation: `violation=<word>`
- Multiple tags are concatenated with `event.TagDelimiter` (`;`) if needed in other scenarios.

## Files of interest
- `hooks.go`: hook implementations (`MarkViolationHook`, `FilterViolationHook`, `FixConsecutiveUserMessagesHook`), tag parsing/append helpers.
- `main.go`: wires hooks into in-memory session service and runs the demo conversation.

## Sample output (abridged)
```text
Using model: qwen3-omni-30b-a3b-thinking
Prohibited words: [pirated serial number crack password]

=== Step 1: Normal request ===
User: Hello, my name is Alice
Assistant: ...
--- Session Events (count=2) ---
[0] user: Hello, my name is Alice
[1] assistant: ...
Hello Alice! How can I assist you today?

=== Step 2: Request with prohibited word ===
[Hook] Marked user message as violation (word: pirated serial number): ...
[Filtered violation: Can you give me a pirated seri...] tag=pirated serial number
[Filtered paired response]
[Hook] Filtered 2 violated event(s)
--- Session Events (count=2) ---
[0] user: Hello, my name is Alice
[1] assistant: ...

=== Step 3: Normal request after violation ===
[Filtered violation: ...] tag=pirated serial number
[Filtered paired response]
[Hook] Filtered 2 violated event(s)
User: What is my name?
Assistant: ...
--- Session Events (count=4) ---
[0] user: Hello, my name is Alice
[1] assistant: ...
[2] user: What is my name?
[3] assistant: Your name is Alice.

=== Step 4: Another normal request ===
... (similar filtered logs)
--- Session Events (count=6) ---
[4] user: Tell me a short joke
[5] assistant: I told my wife she was drawing her eyebrows too ...
```

