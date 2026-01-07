# Finish Result AG-UI Server

This example shows how to populate the `result` field on the `RUN_FINISHED` AG-UI event. It wraps the default `tRPC-Agent-Go` translator, remembers the last model `finish_reason`, and injects it into the final run-finished payload.

## Prerequisites

- An API key for the configured model provider (e.g., `OPENAI_API_KEY` when using `deepseek-chat` via the OpenAI-compatible endpoint).
- `curl` to hit the SSE and history endpoints (see commands below); any AG-UI client will also work once the server is running.

## Run

From the `examples/agui` module:

```bash
# Start the server at http://127.0.0.1:8080/agui with history at /history
go run ./server/finishresult \
  -model deepseek-chat \
  -stream=true \
  -address 127.0.0.1:8080 \
  -path /agui \
  -messages-snapshot-path /history
```

On startup you will see logs similar to:

```
2025-12-03T12:45:43+08:00       INFO    main.go:63  AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
2025-12-03T12:45:43+08:00       INFO    main.go:64  AG-UI: messages snapshot available at http://127.0.0.1:8080/history
```

## Try With curl

### Live Conversation

Send a live request with curl and capture the SSE stream:

```shell
curl --location 'http://127.0.0.1:8080/agui' \
--header 'Content-Type: application/json' \
--data '{
    "threadId": "1",
    "runId": "1",
    "messages": [
        {
            "role": "user",
            "content": "Calculate 123+456"
        }
    ],
    "forwardedProps": {
        "userId": "demo-user"
    }
}' > client.log
```

After the stream finishes, locate the `RUN_FINISHED` payload and inspect the injected `result` field:

```shell
grep -n 'RUN_FINISHED' client.log
```

You should see a `data:` line that includes `"result":"stop"` (or another finish reason depending on the model).

### History Query

Call the messages snapshot endpoint to inspect the persisted messages for the same `(threadId, userId)` pair:

```shell
curl --location 'http://127.0.0.1:8080/history' \
--header 'Content-Type: application/json' \
--data '{
    "threadId": "1",
    "runId": "1",
    "messages": [
        {
            "role": "user",
            "content": ""
        }
    ],
    "forwardedProps": {
        "userId": "demo-user"
    }
}' > history.log
```
