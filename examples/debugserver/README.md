# Debug Server Example

This example shows how to start the trpc-agent-go **Debug Server** and
connect it to [ADK Web](https://github.com/google/adk-web).

## Prerequisites

- Go 1.21 or later
- NodeJS & npm (for running ADK Web UI)

## Running the Server

```bash
# From repository root
cd examples/debugserver

# Start the server with default settings (model: deepseek-chat, addr: :8080)
go run .

# Start the server on custom port
go run . -addr :9090

# Start the server with different model
go run . -model gpt-4

# Start the server with custom model and port
go run . -model gpt-4 -addr :9090
```

### Command Line Options

- `-model`: Name of the model to use (default: "deepseek-chat")
- `-addr`: Listen address (default: ":8080")

## Running ADK Web UI

Clone and serve the front-end (once):

```bash
git clone https://github.com/google/adk-web.git
cd adk-web
npm install

# Point the UI to our Go backend
npm run serve --backend=http://localhost:8080 -- --port=4200 --host=localhost
```

Open <http://localhost:4200> in your browser. In the left sidebar choose the
`assistant` application, create a new session and start chatting. Messages will be
sent to the Go server which streams responses in real-time via the `/run_sse`
endpoint.

---

Feel free to replace the agent logic in `main.go` or add more tools in `tools.go` as needed.
