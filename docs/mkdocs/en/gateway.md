# Gateway Server

The **gateway server** is a small HTTP layer that helps you build an
OpenClaw-like "always-on assistant":

- Messages come from an external surface (HTTP webhook, IM bridge, etc.).
- The gateway turns each message into a `runner.Run()` call.
- The gateway gives every conversation a stable `session_id`.
- The gateway ensures **only one run per session** is executing at a time.

This is useful when you want your agent to behave like a real product:
multi-turn chats, safe defaults for external inputs, and basic run control
(status + cancel).

## Quick start

The gateway server only needs a `runner.Runner`.

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/gateway"
)

ag := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New(
        "deepseek-chat",
        openai.WithVariant(openai.VariantDeepSeek),
    )),
)

r := runner.NewRunner("gateway-demo", ag)
srv, _ := gateway.New(r)

_ = http.ListenAndServe(":8080", srv.Handler())
```

A complete runnable example is available at
[examples/gateway](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/gateway).

An OpenClaw-like demo binary (Telegram long polling + HTTP gateway) is
available at
[openclaw](https://github.com/trpc-group/trpc-agent-go/tree/main/openclaw).

Note: for DeepSeek, the `model/openai` implementation reads `DEEPSEEK_API_KEY`
and defaults to `https://api.deepseek.com` when you set
`openai.WithVariant(openai.VariantDeepSeek)`.

## Endpoints

Default paths:

- `POST /v1/gateway/messages`
- `GET  /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel`
- `GET  /healthz`

### POST /v1/gateway/messages

Send one inbound message.

Minimal request JSON:

```json
{
  "from": "alice",
  "text": "Hello!"
}
```

Fields you can use:

- `channel` (string): optional channel name, defaults to `"http"`.
- `from` (string): sender identity. Also used as `user_id` fallback.
- `thread` (string): optional thread/group identifier.
- `text` (string): the user text.
- `user_id` (string): optional explicit user id used for allowlist checks.
- `session_id` (string): optional override session id.
- `request_id` (string): optional override request id (recommended if you want
  status/cancel while the request is running).

Response JSON:

```json
{
  "session_id": "http:dm:alice",
  "request_id": "req-1",
  "reply": "..."
}
```

If mention gating is enabled and this message is ignored, the response contains:

```json
{"ignored": true}
```

## Session IDs

By default, the gateway derives `session_id` from the inbound message:

- Direct message (no `thread`): `<channel>:dm:<from>`
- Thread message (`thread` set): `<channel>:thread:<thread>`

This makes multi-turn conversations work without you having to generate or
store session ids yourself.

If you need a different strategy, use `gateway.WithSessionIDFunc`.

## Per-session serialization (no concurrent runs)

For the same `session_id`, only one run can execute at a time.

If two HTTP requests arrive concurrently for the same `session_id`, the second
request waits until the first finishes. This keeps:

- Conversation history consistent.
- Tool calls safe (no concurrent writes to the same session state).

Different sessions can still run in parallel.

## Safety defaults

External messages are untrusted input. The gateway provides two common safety
controls:

### 1) User allowlist

To only allow specific users:

```go
srv, _ := gateway.New(r,
    gateway.WithAllowUsers("alice", "bob"),
)
```

When enabled, requests from other users return `403 Forbidden`.

### 2) Mention gating for threads

To ignore thread/group messages unless a mention pattern is present:

```go
srv, _ := gateway.New(r,
    gateway.WithRequireMentionInThreads(true),
    gateway.WithMentionPatterns("@bot"),
)
```

Note: if `WithRequireMentionInThreads(true)` is enabled, you must provide at
least one pattern via `WithMentionPatterns`, otherwise `gateway.New` returns an
error.

When enabled, a message with `thread` set is only processed if `text` contains
any configured pattern.

## Status and cancel

The gateway server exposes:

- `GET /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel` with `{"request_id":"..."}`

Important: if you want to call status/cancel while a run is still in progress,
set `request_id` yourself in the `/messages` request payload so you already
know the id.
