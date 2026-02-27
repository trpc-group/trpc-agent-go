# Runner Plugin + Graph Node Callbacks

This example answers a common question:

> Runner plugins can hook `agent/model/tool/event`.  
> How do I intercept **graph nodes** globally (including function nodes)?

In this project, **graph nodes are not a Runner plugin hook point**.
Graph node execution happens inside the graph engine, and the graph package
provides its own hook system: `graph.NodeCallbacks`.

This example shows an *advanced* pattern:

- Use a **Runner plugin** (`BeforeAgent`) to inject `graph.NodeCallbacks` into
  `Invocation.RunOptions.RuntimeState`.
- The graph engine reads `graph.StateKeyNodeCallbacks` from runtime state and
  treats it as **graph-wide node callbacks** for that run.

## What you will learn

1. How to build a tiny graph with function nodes.
2. How to write `graph.NodeCallbacks` (`BeforeNode` / `AfterNode`).
3. How to inject those callbacks from a Runner plugin.

## Run it

```bash
cd examples/graph/runner_plugin_node_callbacks
go run . -input "hello plugin hooks"
```

## What to look for

- You should see `[node before]` and `[node after]` logs for each node.
- The final answer is produced by the last function node by writing
  `graph.StateKeyLastResponse`.

## When you should *not* use this

If you control the graph construction, prefer the simpler API:

```go
graph.NewStateGraph(schema).
    WithNodeCallbacks(globalCallbacks)
```

The injection approach is mainly for cases where you need runner-scoped
cross-cutting behavior without changing every graph builder.

