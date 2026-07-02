# tRPC-Agent API Server

`server/trpcagent` exposes a local `tRPC-Agent-Go` agent as the tRPC-Agent HTTP API. The server can export the agent structure, forward run requests to a `runner.Runner`, and return events, messages, and execution traces in a unified run response.

It is useful when you want to serve an agent from the current process while allowing external systems to fetch its structure and start runs over HTTP.

## Quick start

Create an agent and runner, then register them with `trpcagent.Server`:

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/log"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

agent := newAgent()
agentRunner := runner.NewRunner("calculator", agent)
defer agentRunner.Close()

server, err := trpcagent.New(
    trpcagent.WithAppName("calculator"),
    trpcagent.WithAgent(agent),
    trpcagent.WithRunner(agentRunner),
)
if err != nil {
    log.Fatalf("create trpc-agent api server failed: %v", err)
}

if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

See [examples/trpcagent/server](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/trpcagent/server) for a complete runnable example.

## Routes

The default base path is `/trpc-agent/v1/apps`. When the app name is `calculator`, the server exposes these routes:

- `GET /trpc-agent/v1/apps/calculator/structure`: exports the current agent structure.
- `POST /trpc-agent/v1/apps/calculator/runs`: starts one agent run and returns the run result.

If only `WithAgent` is configured, the server registers only the structure route. If only `WithRunner` is configured, the server registers only the runs route. This lets each deployment decide whether to expose structure export, run execution, or both.

## Options

`WithAppName` sets the app name exposed by the server. The value is part of the route path and is also written into run options at execution time.

`WithAgent` sets the root agent used for structure export. The structure route builds the snapshot from this agent.

`WithRunner` sets the runner used to execute requests. The runs route converts the request session, input, profile, and run options into one `runner.Run()` call.

`WithBasePath` sets the API route prefix. The default value is `/trpc-agent/v1/apps`. Use it when mounting the server behind an existing HTTP service.

`WithTimeout` sets the timeout for each HTTP request. The timeout is propagated through the request context to structure export and runner execution.

## Requests

Export the structure:

```bash
curl -sS http://127.0.0.1:8080/trpc-agent/v1/apps/calculator/structure
```

Start a run:

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

The `profile` field is optional. When omitted, the runner executes with the current agent configuration. When provided, the server compiles the structured profile into runtime options before execution.
