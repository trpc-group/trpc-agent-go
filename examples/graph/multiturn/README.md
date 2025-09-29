# Graph Multi‑Turn Chat (GraphAgent + Runner)

This example demonstrates an interactive, multi‑turn chat built on the graph package using GraphAgent and Runner. It highlights:

- Persisted conversation history via the session service across turns
- Tool‑augmented LLM chat with a calculator tool and automatic tool routing
- Streaming outputs and interactive REPL experience
- Message‑oriented state with `graph.MessagesStateSchema`

## What It Shows

- Graph‑first design: a single LLM node (`chat`) and a tools node (`tools`), connected with `AddToolsConditionalEdges` so the LLM can call tools when needed, then continue the conversation.
- Natural multi‑turn: every user input is appended to the session; the next run reads full history from the same session so the LLM responds contextually.
- Streaming UX: tokens stream as they arrive, just like other examples.

## Graph Overview

```
Entry: chat (LLM) ──→ tools (when tool calls are present) ──→ back to chat
Finish: chat
```

- Schema: `graph.MessagesStateSchema()`
 - Tool:
   - `calculator(expression)` → evaluates arithmetic expressions (+, -, *, /, ^, parentheses)

## Usage

### Run

```bash
cd examples/graph/multiturn
go run . -model deepseek-chat
```

Then chat interactively:

```
You: hello
Assistant: …streaming…

You: what is (12.5+3)*2?
Assistant: …calls calculator tool and answers…
```

Type `exit` or `quit` to leave.

Notes:
- The example generates a session ID once per process start and reuses it for all turns in the session, so the assistant has memory across turns.
- To persist the same session across process restarts, you can pin `sessionID` to a fixed value in `main.go`.

## Requirements

- Go 1.21+
- Network access and a compatible LLM model (default: `deepseek-chat`). Set any required API keys (e.g., `OPENAI_API_KEY`).

## Key Files

- `main.go` — builds the graph, wraps it with `GraphAgent`, and runs via `Runner` in an interactive loop.

## Related Examples

- `examples/graph/basic` — richer workflow with conditional routing and formatting
- `examples/runwithmessages` — demonstrates Runner + session seeding from a non‑graph flow
