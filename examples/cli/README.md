# CLI Example Server

This example shows how to start the trpc-agent-go **CLI** HTTP server and
connect it to [ADK Web](https://github.com/google/adk-web).

## Prerequisites

* Go 1.24+
* NodeJS & npm (for running ADK Web UI)

## Running the Server

```bash
# From repository root
cd examples/cli

# Start the server on :8080 (default model: deepseek-chat)
go run . -addr :8080
```

## Running ADK Web UI

Clone and serve the front-end (once):

```bash
git clone https://github.com/google/adk-web.git
cd adk-web
npm install

# Point the UI to our Go backend
npm run serve --backend=http://localhost:8080
```

Open <http://localhost:4200> in your browser.  In the left sidebar choose the
`assistant` application, create a new session and start chatting.  Messages will be
sent to the Go server which streams responses in real-time via the `/run_sse`
endpoint.

---

Feel free to replace the agent logic or add more tools in `main.go` as needed. 
