# Session TTL (Time-To-Live) Example

This example demonstrates how the session service automatically expires sessions after a configured TTL duration.

## Overview

When building LLM-powered applications, you may want sessions to expire after a period of inactivity. This example shows how to:

- Configure session TTL (Time-To-Live)
- Verify session expiration behavior
- Handle fresh starts after session expiry

## Usage

```bash
# Run with different backends
go run main.go -session=inmemory
go run main.go -session=redis
go run main.go -session=mysql
go run main.go -session=postgres
go run main.go -session=clickhouse

# Customize TTL (default: 10 seconds)
go run main.go -session=clickhouse -ttl=30
```

## Environment Variables

| Backend    | Variables                                                      |
|------------|----------------------------------------------------------------|
| redis      | `REDIS_ADDR` (default: localhost:6379)                         |
| postgres   | `PG_HOST`, `PG_PORT`, `PG_USER`, `PG_PASSWORD`, `PG_DATABASE`  |
| mysql      | `MYSQL_HOST`, `MYSQL_PORT`, `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_DATABASE` |
| clickhouse | `CLICKHOUSE_HOST`, `CLICKHOUSE_PORT`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_DATABASE` |

## Example Output

```
╔══════════════════════════════════════════════════════════════╗
║           Session TTL (Time-To-Live) Demo                    ║
╚══════════════════════════════════════════════════════════════╝

Backend: clickhouse | TTL: 10s

┌─ Phase 1: Building conversation history ─────────────────────┐
│  User: My name is Alice and I'm a software engineer.
│  Assistant: Nice to meet you, Alice! What kind of software do ...
│  User: I work at TechCorp on distributed systems.
│  Assistant: Cool! Distributed systems can be tricky—what's y...
│  User: What's my name and where do I work?
│  Assistant: Your name is Alice, and you work at TechCorp on di...
│
│  [DEBUG] Session Events: 6
│    1. user     : My name is Alice and I'm a software engineer.
│    2. assistant: Nice to meet you, Alice! What kind of softwar...
│    3. user     : I work at TechCorp on distributed systems.
│    4. assistant: Cool! Distributed systems can be tricky—wha...
│    5. user     : What's my name and where do I work?
│    6. assistant: Your name is Alice, and you work at TechCorp ...
└─ Phase 1 Complete: 6 events stored ─────────────────────────┘

┌─ Phase 2: Waiting for session to expire ─────────────────────┐
│  TTL expired! Session should be cleaned up.                  │
└─ Phase 2 Complete: Session cleaned up ───────────────────────┘

┌─ Phase 3: Fresh conversation after expiry ───────────────────┐
│  (The assistant should NOT remember Alice)                   │
│  User: What's my name?
│  Assistant: I don't have access to personal information unle...
│
│  [DEBUG] Session Events: 2
│    1. user     : What's my name?
│    2. assistant: I don't have access to personal information...
└─ Phase 3 Complete: 2 events (fresh start) ──────────────────┘

=== Demo Complete ===
Verified: session storage, TTL expiration, fresh start after expiry
```

## How It Works

1. **Phase 1**: Build conversation history - assistant remembers user info (Alice at TechCorp)
2. **Phase 2**: Wait for TTL to expire - session is automatically cleaned up
3. **Phase 3**: Start fresh conversation - assistant has no memory of previous conversation

Key behaviors:
- Session expires after TTL duration of inactivity
- Expired sessions return `nil` from `GetSession`
- New conversations with the same session ID create a fresh session
- All events from the expired session are filtered out
