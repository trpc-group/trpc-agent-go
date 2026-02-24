# Gateway Server Example

This example shows how to run an OpenClaw-like **gateway** on top of
`runner.Run()`.

The gateway is a small HTTP service that:

- Accepts inbound messages (a simplified channel payload).
- Derives a stable `session_id` (so multi-turn conversations work).
- Ensures **only one run per session** is executing at a time.
- Returns the final assistant reply as JSON.

## Prerequisites

- Go 1.21 or later

## Run the server

```bash
# From repository root
cd examples/gateway

# Run with a built-in mock model (no API key required)
go run .
```

The server listens on `:8080` by default.

## Send a message (curl)

```bash
curl http://localhost:8080/v1/gateway/messages \
  -H "Content-Type: application/json" \
  -d '{
    "from": "alice",
    "text": "Hello!"
  }'
```

Example response:

```json
{
  "session_id": "http:dm:alice",
  "request_id": "req-1",
  "reply": "Echo: Hello!"
}
```

## Thread messages + mention gating

In many real channels, a `thread` means "group chat". A common safety default
is: **ignore group messages unless the bot is mentioned**.

Start the server with mention gating:

```bash
go run . -require-mention -mention "@bot"
```

Send a thread message without a mention (ignored):

```bash
curl http://localhost:8080/v1/gateway/messages \
  -H "Content-Type: application/json" \
  -d '{
    "from": "alice",
    "thread": "group-1",
    "text": "Hello everyone"
  }'
```

Send a thread message with a mention (processed):

```bash
curl http://localhost:8080/v1/gateway/messages \
  -H "Content-Type: application/json" \
  -d '{
    "from": "alice",
    "thread": "group-1",
    "text": "@bot hello"
  }'
```

## User allowlist (basic access control)

Start the server and only allow specific users:

```bash
go run . -allow-users "alice,bob"
```

Requests from other users return `403 Forbidden`.

## Status + cancel

If you want to poll status or cancel an in-flight run, set `request_id`
yourself so you already know it.

Start the server with a slow mock model:

```bash
go run . -mock-delay 10s
```

Terminal A: start a run (use a fixed request_id):

```bash
curl http://localhost:8080/v1/gateway/messages \
  -H "Content-Type: application/json" \
  -d '{
    "from": "alice",
    "request_id": "req-demo-1",
    "text": "This will take a while"
  }'
```

Terminal B: poll status:

```bash
curl "http://localhost:8080/v1/gateway/status?request_id=req-demo-1"
```

Terminal B: cancel:

```bash
curl http://localhost:8080/v1/gateway/cancel \
  -H "Content-Type: application/json" \
  -d '{"request_id":"req-demo-1"}'
```

