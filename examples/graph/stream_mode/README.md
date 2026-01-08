# StreamMode (LangGraph-style) Example

This example demonstrates `agent.WithStreamMode(...)`, a single per-run switch
that lets Runner filter which categories of events are forwarded to your event
channel.

It uses:

- A GraphAgent workflow (so you get graph events like `graph.node.*`)
- An in-memory checkpoint saver (so you get `graph.checkpoint.*` events)
- A toy in-process model (so it runs without any external Application
  Programming Interface (API) keys)

## What Is StreamMode?

Runner internally processes (and may persist) many kinds of events during a
graph run. StreamMode only controls **what Runner forwards to your `eventCh`**.

Supported modes for graph workflows:

- `messages`: model output events (`chat.completion.chunk`, `chat.completion`)
- `updates`: state/channel execution updates (`graph.state.update`, etc.)
- `checkpoints`: checkpoint lifecycle events (`graph.checkpoint.*`)
- `tasks`: task lifecycle events (`graph.node.*`, `graph.pregel.*`)
- `debug`: same as `checkpoints` + `tasks`
- `custom`: node-emitted custom events (`graph.node.custom`)

Note: Runner always emits a final `runner.completion` event.

## Run

```bash
cd examples/graph/stream_mode
go run .
```

Try different modes:

```bash
go run . -stream-mode messages
go run . -stream-mode updates
go run . -stream-mode checkpoints
go run . -stream-mode tasks
go run . -stream-mode debug
go run . -stream-mode custom
go run . -stream-mode messages,custom
```

When `messages` is selected, Runner also enables final graph Large Language
Model (LLM) response events for that run (so you will see both chunk events and
the final `chat.completion` event).
