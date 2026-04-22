# Default AG-UI Server

This example exposes a minimal AG-UI SSE endpoint backed by the `tRPC-Agent-Go` runner. 

It is intended to be used alongside the [Copilotkit client](../../client/copilotkit/).

## Run

From the `examples/agui` module:

```bash
# Start the server on http://localhost:8080/agui
go run .
```

The server prints startup logs showing the bound address.

```
2025-09-26T10:28:46+08:00       INFO    default/main.go:60      AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
```

## Frontend grouping

If your frontend needs to group streamed tool calls by sub-agent, enable
source metadata on the server:

```go
server, err := agui.New(
    runner,
    agui.WithEventSourceMetadataEnabled(true),
)
```

When enabled, translated AG-UI events carry a compact `rawEvent` object.
Typical fields include:

- `eventId`: the original `trpc-agent-go` event identifier
- `author`: the agent that emitted the original event
- `invocationId`: the concrete invocation that produced the event
- `parentInvocationId`: the parent invocation when the event came from a
  nested sub-agent
- `branch`: the execution branch, useful when the same agent runs multiple
  times in one request

`rawEvent` is optional. It only appears on AG-UI events produced by the
AG-UI translator or the AG-UI message snapshot builder, and it is omitted
when the framework has no non-empty source metadata to expose.

Recommended grouping strategy:

- Use `rawEvent.author` to show one bucket per agent name.
- Use `rawEvent.branch` to show one bucket per concrete sub-agent execution.
