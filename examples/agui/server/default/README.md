# Default AG-UI Server

This example exposes a minimal AG-UI SSE endpoint backed by the `tRPC-Agent-Go` runner. It is intended to be used alongside the Bubble Tea client in `../client/bubbletea`.

## Run

From the `examples/agui` module:

```bash
# Start the server on http://localhost:8080/agui
go run ./server/default
```

The server prints startup logs showing the bound address and the registered runner.
