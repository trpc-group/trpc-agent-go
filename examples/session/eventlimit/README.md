# Session Event Limit Example

This example demonstrates how the session service limits the number of events stored per session using a sliding window mechanism.

## Overview

When building LLM-powered applications, conversation history can grow indefinitely. This example shows how to:

- Set a maximum number of events per session
- Use sliding window to keep only the most recent events
- Control memory usage and LLM context length

## Usage

```bash
# Run with different backends
go run main.go -session=inmemory
go run main.go -session=redis
go run main.go -session=mysql
go run main.go -session=postgres
go run main.go -session=clickhouse

# Customize event limit (default: 4)
go run main.go -session=clickhouse -limit=6
```

## Example Output

```
╔══════════════════════════════════════════════════════════════╗
║           Session Event Limit Demo                           ║
╚══════════════════════════════════════════════════════════════╝

Backend: clickhouse | Event Limit: 4

┌─ Phase 1: Building conversation (will exceed limit) ─────────┐
│  Event limit: 4 (= 2 conversation turns)                    │
│
│  [Turn 1]
│  User: My name is Alice.
│  Assistant: Nice to meet you, Alice! How can I help you today?
│  -> Events in session: 2
│
│  [Turn 2]
│  User: I live in Beijing.
│  Assistant: Beijing is a great city! What brings you here toda...
│  -> Events in session: 4
│
│  [Turn 3]
│  User: I work as a software engineer.
│  Assistant: That's a cool job! Do you need help with anything ...
│  -> Events in session: 4
│
│  [Turn 4]
│  User: My favorite color is blue.
│  Assistant: Nice choice! Blue is calming and versatile.
│  -> Events in session: 4
│
└─ Phase 1 Complete ───────────────────────────────────────────┘

┌─ Phase 2: Verify sliding window ─────────────────────────────┐
│
│  [DEBUG] Session Events: 4
│    1. user     : I work as a software engineer.
│    2. assistant: That's a cool job! Do you need help with anyt...
│    3. user     : My favorite color is blue.
│    4. assistant: Nice choice! Blue is calming and versatile.
│
│  [OK] Event count (4) <= limit (4)
└─ Phase 2 Complete ───────────────────────────────────────────┘

┌─ Phase 3: Test what the assistant remembers ─────────────────┐
│  (Early messages should be forgotten due to sliding window)  │
│
│  Testing: recent - should remember
│  User: What's my favorite color?
│  Assistant: Your favorite color is blue!
│
│  Testing: early - may be forgotten
│  User: What's my name?
│  Assistant: I don't know your name—you haven't told me y...
│
└─ Phase 3 Complete ───────────────────────────────────────────┘

=== Demo Complete ===
Verified: event limit enforced (max 4), sliding window preserves recent context
```

## How It Works

1. **Event Limit**: Each conversation turn creates 2 events (user message + assistant response)
2. **Sliding Window**: When limit is reached, oldest events are dropped
3. **Context Retention**: Only recent events are available to the LLM

In this example with limit=4:
- Turns 1-2: Events accumulate (2 → 4 events)
- Turns 3-4: Sliding window kicks in, oldest events dropped
- Result: Assistant remembers "favorite color" (recent) but forgets "name" (early)
