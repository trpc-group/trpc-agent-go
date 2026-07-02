# tRPC-Agent API Server

This example exposes a local `tRPC-Agent-Go` agent through the `server/trpcagent` HTTP server.

It registers one app named `calculator` and exposes:

- `GET /trpc-agent/v1/apps/calculator/structure`
- `POST /trpc-agent/v1/apps/calculator/runs`

## Run

From the `examples` module:

```bash
go run ./trpcagent/server
```

Use a custom app name or base path when embedding the server behind another HTTP service:

```bash
go run ./trpcagent/server \
  -app calculator \
  -base-path /trpc-agent/v1/apps \
  -address 127.0.0.1:8080
```

## Fetch structure

```bash
curl -sS http://127.0.0.1:8080/trpc-agent/v1/apps/calculator/structure
```

## Run

```bash
curl -sS http://127.0.0.1:8080/trpc-agent/v1/apps/calculator/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "session": {
      "userId": "alice",
      "sessionId": "demo-session"
    },
    "input": {
      "role": "user",
      "content": "Use the calculator to compute 12 * 7."
    },
    "runOptions": {
      "requestID": "demo-run-1",
      "executionTraceEnabled": true
    }
  }'
```

The model provider configuration follows the `model/openai` package defaults, such as `OPENAI_API_KEY` and compatible base URL environment variables.
