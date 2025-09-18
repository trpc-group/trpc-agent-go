# Bubbletea AG-UI Demo

This example pairs a minimal AG-UI SSE server powered by `llmagent` with a
lightweight terminal client. The demo streams real model responses via the
AG-UI protocol.

## Layout

- `server/` – AG-UI SSE server backed by an OpenAI-compatible LLM agent.
- `client/` – Simple terminal client that reads a prompt, streams AG-UI events,
  and prints the assistant output.

## Prerequisites

- Go 1.24+
- An OpenAI-compatible API key (export `OPENAI_API_KEY`).

## Run the server

```bash
export OPENAI_API_KEY=sk-...
cd examples/agui
GO111MODULE=on go run ./bubbletea/server/cmd
```

The server listens on `http://localhost:8080/agui/run` by default. Adjust the
model name in `server/cmd/main.go` if you prefer a different backend.

## Run the client

In a second terminal:

```bash
cd examples/agui
GO111MODULE=on go run ./bubbletea/client
```

Type a message and press Enter. The client streams AG-UI events from the server
and prints the assistant response. Press Enter on an empty line to exit.

Example session:

```
╭──────────────────────────────────────────────────────────────╮
│                                                              │
│  Simple AG-UI Client. Press Ctrl+C to quit.                  │
│  You> calculate 1.25^0.42                                    │
│  Agent> [RUN_STARTED]                                        │
│  Agent> [TOOL_CALL_START] tool call 'calculator' started,    │
│  Agent> [TOOL_CALL_ARGS] tool args: {"a":1.25,"b":0.42,"operation":"power"}
│  Agent> [TOOL_CALL_END] tool call completed, id: call_00_... │
│  Agent> [TOOL_CALL_RESULT] tool result: {"result":1.09825...}│
│  Agent> [TEXT_MESSAGE_START]                                 │
│  Agent> [TEXT_MESSAGE_CONTENT] The result of 1.25^0.42 is... │
│  Agent> [TEXT_MESSAGE_END]                                   │
│  Agent> [RUN_FINISHED]                                       │
│                                                              │
╰──────────────────────────────────────────────────────────────╯
```
