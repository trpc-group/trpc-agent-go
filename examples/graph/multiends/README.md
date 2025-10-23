# Multi-Ends Branching Example

This example demonstrates per-node named ends ("multi-ends") and how branches resolve locally at a node using symbolic labels that map to concrete destinations.

## Overview

- Per-node named ends: Declare branch labels on a node and map them to concrete destinations (node IDs or the special `End`).
- Clearer branch semantics: Nodes return symbolic results (e.g., `approve`, `reject`) and the graph resolves them via the node’s ends map.
- Stronger validation: Ends mappings are validated at compile time — targets must exist or be `End`.

### Graph Structure

```
start -> decide --approve--> approved -> final
                 \
                  --reject--> rejected -> final
```

- `decide` declares ends: `{ "approve": "approved", "reject": "rejected" }`.
- The node returns `&Command{GoTo: "approve"}` or `&Command{GoTo: "reject"}` and routing is resolved via its ends.

## What This Example Shows

- Defining per-node ends using `WithEndsMap`.
- Returning symbolic branch keys via `Command.GoTo`.
- Compile-time validation of end targets.

## Usage

Run with an approval decision (default):

```
go run .
```

Run with an explicit choice:

```
go run . -choice=approve

go run . -choice=reject
```

Example output (approve):

```
🚀 Multi-Ends Branching Example
✅ Finished. path=approved, result=completed via approved
```

Example output (reject):

```
🚀 Multi-Ends Branching Example
✅ Finished. path=rejected, result=completed via rejected
```

## Implementation Notes

- The `decide` node uses `WithEndsMap(map[string]string{"approve":"approved","reject":"rejected"})`.
- The node returns a symbolic branch via `&graph.Command{GoTo: "approve"}` or `"reject"`.
- The executor resolves the symbolic key using the node’s ends, then triggers the concrete destination.
- Targets are validated at compile time during `Compile()`.

## Related Examples

- `examples/graph/basic` – Building graphs, conditional routing, and tool integration
- `examples/graph/diamond` – Fan-out/fan-in with barriers and checkpoint/resume
- `examples/graph/per_node_callbacks` – Global and per-node callbacks
