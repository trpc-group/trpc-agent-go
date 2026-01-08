# StreamMode Example

This example demonstrates `agent.WithStreamMode(...)`, a single per-run switch
that lets Runner filter which categories of events are forwarded to your event
channel.

It uses:

- A GraphAgent workflow (so Runner sees graph events like `graph.node.*`)
- A node that emits OpenAI-style message events (`chat.completion.*`)
- No external Application Programming Interface (API) keys (everything runs
  locally)

## What Is StreamMode?

Runner internally processes (and may persist) many kinds of events during a
graph run. StreamMode controls **what Runner forwards to your `eventCh`**.
For graph workflows, some event types (for example, `graph.checkpoint.*`) are
emitted only when their corresponding mode is selected.

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

To try different modes, edit the `agent.WithStreamMode(...)` call in `main.go`
and re-run.

When `messages` is selected, Runner forwards only message events
(`chat.completion.chunk`, `chat.completion`) plus the final `runner.completion`
event. For graphs that contain real Large Language Model (LLM) nodes, messages
mode also enables final model response events for that run by default.
