# OnDuplicateUserMessage Handler Demo

This example demonstrates how to handle consecutive user messages using `OnDuplicateUserMessageFunc` handlers.

## What it demonstrates

When network issues, retries, or race conditions cause two consecutive user messages without an assistant response in between, the session history becomes invalid. This example shows three strategies to fix this:

1. **Insert Placeholder** (`-handler=placeholder`): Inserts a placeholder assistant message `[Connection interrupted]` between consecutive user messages.
2. **Remove Previous** (`-handler=remove`): Removes the first (older) user message, keeping only the newer one.
3. **Skip Current** (`-handler=skip`): Skips the current (newer) user message, keeping only the older one.

## Prerequisites

- Go 1.21+
- LLM endpoint/key (OpenAI-compatible). Set:
  - `OPENAI_API_KEY`
  - `OPENAI_BASE_URL` (default `https://api.openai.com/v1`)
- Optional: `MODEL_NAME` (default `deepseek-chat`)

## Quick start

```bash
cd examples/session/onduplicateuser
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

# Try different handlers
go run . -handler=placeholder -session=inmemory
go run . -handler=remove -session=inmemory
go run . -handler=skip -session=inmemory
```

## Command-line flags

- `-handler`: Handler type (`placeholder` / `remove` / `skip`)
- `-session`: Session backend (`inmemory` / `redis` / `postgres` / `mysql` / `clickhouse`)
- `-model`: Model name (default: `deepseek-chat` or `$MODEL_NAME`)

## Expected flow

1. **Step 1**: Normal first request → stores user message and assistant response.
2. **Step 2**: Simulate consecutive user messages (no assistant response):
   - Handler detects the issue
   - Applies the selected fix strategy
3. **Step 3**: Continue normal conversation with fixed history.

## Handler behaviors

### Placeholder Handler

**Before fix:**
```
[0] user: Hello, my name is Alice
[1] assistant: Hello Alice! How can I assist you today?
[2] user: How are you?
[3] user: Are you there?  ← Invalid!
```

**After fix:**
```
[0] user: Hello, my name is Alice
[1] assistant: Hello Alice! How can I assist you today?
[2] user: How are you?
[3] assistant: [Connection interrupted]  ← Placeholder inserted
[4] user: Are you there?
```

### Remove Previous Handler

**Before fix:**
```
[2] user: How are you?
[3] user: Are you there?  ← Invalid!
```

**After fix:**
```
[2] user: Are you there?  ← Kept newer message
```

### Skip Current Handler

**Before fix:**
```
[2] user: How are you?
[3] user: Are you there?  ← Invalid!
```

**After fix:**
```
[2] user: How are you?  ← Kept older message
```

## Files of interest

- `handlers.go`: Implementation of the three handler strategies.
- `main.go`: Demonstrates each handler with a simulated duplicate user message scenario.

## Sample output (placeholder handler)

```text
Using model: deepseek-chat
Session backend: inmemory
Handler type: placeholder

=== Step 1: Normal first request ===
User: Hello, my name is Alice
Assistant: Hello Alice! How can I assist you today?
--- Session Events (count=2) ---
[0] user: Hello, my name is Alice
[1] assistant: Hello Alice! How can I assist you today?

=== Step 2: Simulate duplicate user message (no assistant response) ===
[Using InsertPlaceholderHandler]
Appending first user message: How are you?
Appending second user message: Are you there?
[Handler triggered and fixed the consecutive user messages]
--- Session Events (count=5) ---
[0] user: Hello, my name is Alice
[1] assistant: Hello Alice! How can I assist you today?
[2] user: How are you?
[3] assistant: [Connection interrupted]
[4] user: Are you there?

=== Step 3: Normal request after fixing ===
User: What is my name?
Assistant: Your name is Alice.
--- Session Events (count=7) ---
[0] user: Hello, my name is Alice
[1] assistant: Hello Alice! How can I assist you today?
[2] user: How are you?
[3] assistant: [Connection interrupted]
[4] user: Are you there?
[5] user: What is my name?
[6] assistant: Your name is Alice.
```

## Use cases

- **Network retry scenarios**: When a user's request is retried due to network issues.
- **Race conditions**: When multiple user messages are sent before the assistant responds.
- **Connection interruptions**: When the connection drops during assistant response.
- **Client-side bugs**: When client code accidentally sends consecutive user messages.

## Implementation notes

- Handlers receive the session and both user events (previous and current).
- Handlers can modify `sess.Events` directly to insert, remove, or merge events.
- Return `true` to append the current event, `false` to skip it.
- The handler is called **before** the current event is appended to the session.
