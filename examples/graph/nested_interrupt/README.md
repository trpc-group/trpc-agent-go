# Nested Human-in-the-Loop (HITL) Interrupt (Multi-level GraphAgent)

This example shows a nested graph setup with **multiple levels**:

- A **parent** graph runs a sub-agent node.
- The sub-agent is a **GraphAgent** (a graph-based agent).
- That GraphAgent can call another GraphAgent, forming a chain.
- The deepest (leaf) graph pauses the run by calling `graph.Interrupt(...)`.

The important behavior is:

When a child graph interrupts, the parent graph also interrupts, so you can
resume from the **parent checkpoint** and the framework will automatically
resume **inside the nested child graph(s)**.

## What You Will See

1. `run` mode stops at a human prompt and prints a checkpoint identifier (ID).
2. `resume` mode uses that checkpoint ID and a resume value to complete.

## How It Works (High Level)

- A **graph** is a set of nodes (functions) connected by edges.
- A **GraphAgent** is an Agent that executes a graph.
- `graph.Interrupt` stops execution by returning a special error.
- With a **checkpoint** saver, the engine writes a checkpoint (a saved state
  snapshot) at the interrupt point.
- Each parent agent node detects the child interrupt event and triggers a
  parent interrupt checkpoint that remembers how to resume the child.

This example also demonstrates the difference between:

- Node Identifier (Node ID): where the graph paused (in the current graph).
- Task Identifier (Task ID): the resume key used by `ResumeMap`. For
  `graph.Interrupt(ctx, state, key, prompt)`, the Task ID equals `key`.

This example uses a local Structured Query Language (SQL) database (SQLite) as
the checkpoint storage so you can run `run` and `resume` as separate commands.

## Run

```bash
cd examples/graph/nested_interrupt

# 1) Start (will interrupt). Depth 2 means: parent -> 1 child GraphAgent.
go run . -mode run -depth 2 -lineage-id demo

# Or try depth 3 (parent -> child -> grandchild):
go run . -mode run -depth 3 -lineage-id demo

# 2) Resume (replace with the checkpoint ID printed above)
go run . -mode resume -depth 3 -lineage-id demo \
  -checkpoint-id <checkpoint-id> \
  -resume-value approved
```

## Flags

- `-mode`: `run` or `resume`
- `-depth`: how many GraphAgents are nested (must be ≥ 2)
- `-lineage-id`: stable identifier used to group checkpoints
- `-db`: SQLite database file path
- `-checkpoint-id`: required for `resume`
- `-resume-value`: value returned by `graph.Interrupt` on resume (this example
  uses `ResumeMap` internally and routes it by Task ID)

## Files

- `examples/graph/nested_interrupt/main.go`
