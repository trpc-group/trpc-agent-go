# A2A Protocol v1.0 Example

This example runs a session-aware LLM agent behind the A2A protocol v1.0
server adapter and connects to it through the matching Agent adapter. The
server and client are separate programs.

The A2A server uses an in-memory trpc-agent-go session service. Requests with
the same user and session ID share conversation context even though the
default A2A task manager retains Tasks only for the lifetime of each request.

## Prerequisites

Configure an OpenAI-compatible model:

```bash
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>"
export MODEL_NAME="<your-model>"
```

## Run

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
