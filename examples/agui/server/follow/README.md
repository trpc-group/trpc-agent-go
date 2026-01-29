# MessagesSnapshot Follow AG-UI Server

This example exposes both the regular AG-UI SSE endpoint and the message snapshot endpoint, with follow mode enabled.

When a client calls `POST /history` while the `threadId` is still producing events, the server will:

1. Emit `MESSAGES_SNAPSHOT`.
2. Continue streaming the subsequent persisted events until the run finishes.

## Run

From the `examples/agui` module:

```bash
cd server/follow
go run .
```

Endpoints (by default):

- Chat endpoint: `http://127.0.0.1:8080/agui`
- Snapshot endpoint: `http://127.0.0.1:8080/history`

## Verify follow behaviour

1) Start a long chat stream (Terminal A):

```bash
curl -N -H 'Content-Type: application/json' \
  -d '{
    "threadId": "demo-thread",
    "runId": "live-run",
    "messages": [
      {
        "role": "user",
        "content": "Please write a very long story so the response streams for a while."
      }
    ]
  }' \
  http://127.0.0.1:8080/agui
```

2) While the chat is still streaming, request history (Terminal B):

```bash
curl -N -H 'Content-Type: application/json' \
  -d '{
    "threadId": "demo-thread",
    "runId": "history-run"
  }' \
  http://127.0.0.1:8080/history
```

You should see:

- `RUN_STARTED` (synthetic history run)
- `MESSAGES_SNAPSHOT`
- Followed by additional `TEXT_MESSAGE_*` events until `RUN_FINISHED`

## Notes

This example uses an in-memory session store and is intended for single-process demos.
