# External Tool Client

This client runs the two-call external tool workflow against the `server/externaltool` AG-UI example.

It performs:

- Call 1: send a `role=user` message, capture `toolCallId` and tool args from the SSE stream.
- Call 2: send a `role=tool` message with the external tool result, and stream the resumed answer.

## Prerequisites

Start the server:

```bash
cd examples/agui
go run ./server/externaltool
```

## Run

In a new terminal:

```bash
cd examples/agui
go run ./client/externaltool
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-endpoint` | `http://127.0.0.1:8080/agui` | AG-UI SSE endpoint |
| `-thread` | `demo-thread` | `threadId` used for both calls |
| `-lineage` | `demo-lineage` | `forwardedProps.lineage_id` used for checkpoint resume |
| `-question` | `What is trpc-agent-go?` | Call 1 user question |
| `-tool-content` | (auto) | Call 2 tool result content |
| `-tool-message-id` | (auto) | Call 2 tool message `id` (`TOOL_CALL_RESULT.messageId`) |
| `-run1-log` | (empty) | Save call 1 SSE `data:` lines to a file |
| `-run2-log` | (empty) | Save call 2 SSE `data:` lines to a file |

### Example

```bash
go run ./client/externaltool \
  -question "Search for tRPC-Agent-Go and summarize." \
  -run1-log ./run1.log \
  -run2-log ./run2.log
```

