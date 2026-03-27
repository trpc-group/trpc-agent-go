# Graph Structure Export Example

This example demonstrates how to export a static structure snapshot from a `GraphAgent` and print it in a user-friendly way.

## What it demonstrates

- A single graph that contains:
  - fan-out
  - fan-in
  - conditional edges
  - a loop
  - a join edge
- Exporting the graph as a normalized `structure.Snapshot`
- Printing:
  - basic snapshot metadata
  - nodes
  - edges
  - surfaces
  - derived highlights such as branch points, fan-in points, and loop regions

## File layout

- `main.go`: Program entrypoint
- `agent.go`: Graph construction and `GraphAgent` creation
- `print.go`: Snapshot formatting and user-friendly output

## Run

From the repo root:

```bash
cd examples/graph
go run ./structure_export
```

This example does not execute any model request. It only builds a `GraphAgent` and exports its static structure, so it does not require API credentials even though it uses a real model instance in the graph definition.

## What to look for

The output is divided into four parts:

- `Nodes`: Stable static nodes in the exported graph
- `Edges`: Static possible connections between nodes
- `Surfaces`: Editable static baselines such as instruction, model, and tools
- `Highlights`: A compact summary derived from the snapshot

Typical highlights in this example include:

- `start` fan-out into `route` and `prepare`
- `branch_a` and `branch_b` joining at `join`
- `join` conditionally routing to `done` or back to `start`
- a loop region formed by `start`, `route`, `tools`, `prepare`, `branch_a`, `branch_b`, and `join`

## Example output

```text
GraphAgent static structure snapshot
========================================================================
Structure ID: struct_742fe9bdd46f54d64e52fd779495a872aea65292c434888bdd9463d7375c16e1
Entry Node:   assistant
Node Count:   9
Edge Count:   11
Surface Count: 4

Nodes
- assistant [agent] name=assistant
- assistant/branch_a [function] name=branch_a
- assistant/branch_b [function] name=branch_b
- assistant/done [function] name=done
- assistant/join [function] name=join
- assistant/prepare [function] name=prepare
- assistant/route [llm] name=route
- assistant/start [function] name=start
- assistant/tools [tool] name=tools

Edges
- assistant -> assistant/start
- assistant/branch_a -> assistant/join
- assistant/branch_b -> assistant/join
- assistant/join -> assistant/done
- assistant/join -> assistant/start
- assistant/prepare -> assistant/branch_b
- assistant/route -> assistant/branch_a
- assistant/route -> assistant/tools
- assistant/start -> assistant/prepare
- assistant/start -> assistant/route
- assistant/tools -> assistant/route

Surfaces
- assistant/route
  - instruction: "Route the request. Use tools when more evidence is needed, otherwise continue directly to the business branch."
  - model: gpt-4o-mini
  - tool: search_docs (Look up reference snippets before taking the next branch.), summarize (Summarize intermediate findings before returning to routing.)
- assistant/tools
  - tool: search_docs (Look up reference snippets before taking the next branch.), summarize (Summarize intermediate findings before returning to routing.)

Highlights
These summaries are derived from static possible edges.
- Branch points
  - assistant/join can branch to assistant/done, assistant/start
  - assistant/route can branch to assistant/branch_a, assistant/tools
  - assistant/start can branch to assistant/prepare, assistant/route
- Fan-in points
  - assistant/join can receive input from assistant/branch_a, assistant/branch_b
  - assistant/route can receive input from assistant/start, assistant/tools
- Loop regions
  - assistant/branch_a, assistant/branch_b, assistant/join, assistant/prepare, assistant/route, assistant/start, assistant/tools form one loop region
```

## Related APIs

- `graph.NewStateGraph`
- `graph.StateGraph.AddConditionalEdges`
- `graph.StateGraph.AddJoinEdge`
- `graphagent.New`
- `structure.Export`
