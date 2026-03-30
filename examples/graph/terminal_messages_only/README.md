# Terminal Graph Messages Only

This example shows how `agent.WithGraphTerminalMessagesOnly(true)` changes the
caller-visible message stream for a graph that contains multiple sub-agent
nodes.

The parent graph runs two child agents in sequence:

- `draft`: emits an intermediate draft message
- `final`: emits the user-visible answer

Both child agents still run in both modes. The only difference is what the
caller sees on the event stream.

## What the option does

- Default behavior: all graph message events are forwarded, including
  intermediate sub-agent output.
- Terminal-only behavior: only terminal graph message events are forwarded.
- Internal graph behavior does not change. State handoff still uses the full
  graph execution, so the `final` agent still receives the `draft` output via
  `graph.WithSubgraphInputFromLastResponse()`.

This is useful when product UI should stream only the last user-facing graph
step, while keeping intermediate graph messages internal.

## Run

```bash
cd examples/graph/terminal_messages_only
go run .
```

No external API key is required.

## Expected output

The program runs the same graph twice.

With the default behavior, both child agents are visible:

```text
== default ==
parent/draft chunk: draft: collecting context
parent/draft final: draft: collecting context
parent/final chunk: final: user-visible answer
parent/final final: final: user-visible answer
```

With `agent.WithGraphTerminalMessagesOnly(true)`, only the terminal sub-agent
remains visible:

```text
== terminal-only ==
parent/final chunk: final: user-visible answer
parent/final final: final: user-visible answer
```

## Notes

- For real LLM nodes, combine this option with
  `agent.WithGraphEmitFinalModelResponses(true)` if you also want terminal
  `Done=true` assistant message events to be forwarded.
- Parallel terminal nodes are all preserved. This option does not collapse a
  fan-out graph into a single winner.
