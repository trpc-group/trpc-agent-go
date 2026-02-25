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

This repo includes an OpenClaw-like demo binary that runs the gateway server:
[openclaw](https://github.com/trpc-group/trpc-agent-go/tree/main/openclaw).

Run with a mock model (no external credentials needed):

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080
```

Health check:

```bash
curl -sS 'http://127.0.0.1:8080/healthz'
```

Send one message via HTTP (webhook-style):

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

You can also enable Telegram long polling in the same binary (see
`openclaw/README.md`).

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

If you need a different strategy, set `session_id` explicitly in the request
payload.

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

To only allow specific users (openclaw demo):

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -allow-users "123456789,987654321"
```

When enabled, other users get `403 Forbidden` (HTTP) or are dropped by
Telegram.

### 2) Mention gating for threads

To ignore thread/group messages unless a mention pattern is present
(openclaw demo):

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -require-mention \
  -mention "@mybot,/agent"
```

When enabled, a message with `thread` set is only processed if `text` contains
any configured pattern.

## Relationship to OpenClaw "Gateway protocol"

The "Gateway protocol" documented by OpenClaw refers to a **WebSocket
control-plane protocol** (with concepts like device / role / approvals /
pairing). It is not something you get by simply renaming HTTP JSON fields.

The gateway server in this repo is intentionally a **minimal data-plane HTTP
API**: it maps one inbound message to one `runner.Run()` call, and provides
basic run control (status + cancel).

If OpenClaw client compatibility becomes important, the better approach is to
add a WS control-plane (or a protocol adapter) in the `openclaw/` demo binary,
while keeping `/v1/gateway/*` stable and small.

## Status and cancel

The gateway server exposes:

- `GET /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel` with `{"request_id":"..."}`

Important: if you want to call status/cancel while a run is still in progress,
set `request_id` yourself in the `/messages` request payload so you already
know the id.
