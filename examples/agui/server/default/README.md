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
