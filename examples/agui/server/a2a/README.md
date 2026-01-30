# A2A AG-UI Server

This example exposes an AG-UI SSE endpoint backed by an A2A agent.

It demonstrates how to:
1. Create a local A2A server with an LLM agent that has tools (getCurrentTime, calculate)
2. Create an A2A agent client that connects to the A2A server
3. Expose the A2A agent through AG-UI protocol for CopilotKit frontend

This is intended to be used alongside the [Copilotkit client](../../client/copilotkit/).

## Run

From the `examples/agui` module:

```bash
# Start the server with default settings
# - A2A server on http://127.0.0.1:8888
# - AG-UI server on http://127.0.0.1:8080/agui
go run ./server/a2a
```

### Command Line Options

```bash
go run ./server/a2a \
  -model deepseek-chat \
  -a2a-host 127.0.0.1:8888 \
  -agui-address 127.0.0.1:8080 \
  -agui-path /agui \
  -streaming=true
```

| Flag | Default | Description |
|------|---------|-------------|
| `-model` | `deepseek-chat` | Model name to use |
| `-a2a-host` | `127.0.0.1:8888` | A2A server listen address |
| `-agui-address` | `127.0.0.1:8080` | AG-UI server listen address |
| `-agui-path` | `/agui` | AG-UI HTTP path |
| `-streaming` | `true` | Enable streaming mode |

## Output

The server prints startup logs showing the bound addresses:

```
2025-01-16T10:28:46+08:00       INFO    a2a/main.go:49  Starting A2A server on 127.0.0.1:8888
2025-01-16T10:28:46+08:00       INFO    a2a/main.go:106 A2A server listening on 127.0.0.1:8888
2025-01-16T10:28:46+08:00       INFO    a2a/main.go:55  Creating A2A agent client for: http://127.0.0.1:8888
2025-01-16T10:28:46+08:00       INFO    a2a/main.go:66  Connected to A2A agent: a2a-demo-agent
2025-01-16T10:28:46+08:00       INFO    a2a/main.go:83  AG-UI: serving A2A agent "a2a-demo-agent" on http://127.0.0.1:8080/agui
```

## Available Tools

The A2A agent provides two tools:

- **getCurrentTime**: Get current time for a specific timezone (UTC, EST, PST, CST)
- **calculate**: Perform basic arithmetic (add, subtract, multiply, divide)
