# StreamMode Example

This example demonstrates `agent.WithStreamMode(...)`, a single per-run switch
that lets Runner filter which categories of events are forwarded to your event
channel.

It uses:

- A GraphAgent workflow (so Runner sees graph events like `graph.node.*`)
- A node that emits message events (`chat.completion.*`) and progress snapshots
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
- `progress`: events carrying the common `event.Progress` extension across producers

This example enables `agent.WithProgressEvents(true)` and subscribes to
`messages` + `progress`. The production switch defaults to false and is
independent of latency diagnostics and tracing. Selecting `progress` alone
does not enable production. Unfiltered runs forward generated progress too.

Each retained graph emitter has one operation ID. Its `EmitProgress` calls
remain `running` snapshots, including at 100%; the percentage does not assert
successful node completion. A new emitter represents a new operation. The
legacy `graph.node.custom` payload is preserved, so `custom` + `progress`
forwards the same event once.

Runner always emits a final `runner.completion` event. Consumers should use
the run outcome to reconcile unfinished operations after cancellation or when
a producer only supplies snapshots. See the [Runner progress contract](../../../docs/mkdocs/en/runner.md#unified-progress)
for compaction, custom producers, and protocol adapters.

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

If your graph contains multiple user-facing message nodes and you want only the
terminal graph messages to remain visible, combine StreamMode with
`agent.WithGraphTerminalMessagesOnly(true)`. See
`examples/graph/terminal_messages_only`.
