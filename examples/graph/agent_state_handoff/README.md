# Agent Node: Pass State to Later Nodes

This example answers a common question:

> If a graph has an Agent node, how can the agent (the sub-agent) pass data
> through state to the nodes that run after it?

## What this example demonstrates

- A parent graph calls a child agent using an Agent node.
- The child agent writes a value into its own graph state.
- The parent graph uses `WithSubgraphOutputMapper` to copy a value from the
  child agent’s final state back into the parent graph state.
- A later parent node reads that copied value from state and uses it.

## The key mechanism: `WithSubgraphOutputMapper`

An Agent node runs a sub-agent. The sub-agent has its own state.

To pass information back to the parent graph, attach an output mapper:

- **Child writes**: `child_value` into its state.
- **Output mapper copies**: `child_value` → `value_from_child` in parent state.
- **Later parent node reads**: `value_from_child`.

## Run

From this directory:

```bash
go run .
```

You can also pass your own input text:

```bash
go run . -input "hello graph"
```

## Expected output (example)

You should see three lines:

- The original input
- The value copied from the child agent back into parent state
- The final response produced by the last parent node

## Files

- `examples/graph/agent_state_handoff/main.go`

