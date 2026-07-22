# A2A Protocol v1.0 Example

This example runs a session-aware LLM agent behind the A2A protocol v1.0
server adapter. It includes two client cases:

- `client` uses the Agent adapter for session-aware chat.
- `taskclient` uses the A2A client directly to exercise retained Task APIs.

The server creates a trpc-agent-go Runner with an in-memory session service,
then exposes it through `WithRunner` and an explicit Agent Card. Requests with
the same user and session ID share conversation context even though the default
A2A task manager retains Tasks only for the lifetime of each request. Pass
`-retain-tasks` to replace it with the memory TaskManager when the A2A task
control plane is needed.

## Prerequisites

Configure an OpenAI-compatible model:

```bash
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>"
export MODEL_NAME="<your-model>"
```

## Session-aware Agent client

Start the server from the `examples` module:

```bash
cd examples
go run ./a2aagent/v1/server
```

In another terminal, start the client:

```bash
cd examples
go run ./a2aagent/v1/client
```

The client keeps using the same session ID until you switch it:

- `/new [id]` starts a new session
- `/use <id>` switches to an existing session
- `/exit` exits the client

Use `-streaming=false` on the server to exercise blocking `message/send`.
The client discovers the streaming capability from the Agent Card.

The example uses in-memory session storage, so restarting the server clears its
conversation history.

## Retained A2A Task case

Start the same server with the memory TaskManager enabled:

```bash
cd examples
go run ./a2aagent/v1/server -retain-tasks
```

Then run the direct A2A task client:

```bash
cd examples
go run ./a2aagent/v1/taskclient \
  -prompt "Explain the A2A task lifecycle."
```

The task client sends `message/send` with `returnImmediately=true`, receives an
immediate Task snapshot, polls it with `tasks/get`, and finally verifies it with
`tasks/list`. The memory TaskManager also enables retained lookup, cancellation,
and resubscription. Continuation still requires a processor that emits an
interrupted state, and push delivery requires additional push configuration.

Session state and A2A Task state remain independent: the Runner's session
service owns conversation context, while the memory TaskManager retains A2A
Task lifecycle state. Both are cleared when the server process restarts.
