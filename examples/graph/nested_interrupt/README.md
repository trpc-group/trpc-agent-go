# Nested Human-in-the-Loop (HITL) Interrupt (Parent → Child GraphAgent)

This example shows a nested graph setup:

- A **parent** graph runs a sub-agent node.
- The sub-agent is a **child GraphAgent** (a graph-based agent).
- The **child** pauses the run by calling `graph.Interrupt(...)`.

The important behavior is:

When the child graph interrupts, the parent graph also interrupts, so you can
resume from the **parent checkpoint** and the framework will automatically
resume **inside the child graph**.

## What You Will See

1. `run` mode stops at a human prompt and prints a checkpoint identifier (ID).
2. `resume` mode uses that checkpoint ID and a resume value to complete.

## How It Works (High Level)

- A **graph** is a set of nodes (functions) connected by edges.
- A **GraphAgent** is an Agent that executes a graph.
- `graph.Interrupt` stops execution by returning a special error.
- With a **checkpoint** saver, the engine writes a checkpoint (a saved state
  snapshot) at the interrupt point.
- The parent agent node detects the child interrupt event and triggers a parent
  interrupt checkpoint that remembers how to resume the child.

This example uses a local Structured Query Language (SQL) database (SQLite) as
the checkpoint storage so you can run `run` and `resume` as separate commands.

## Run

```bash
cd examples/graph/nested_interrupt

# 1) Start (will interrupt)
go run . -mode run -lineage-id demo

# 2) Resume (replace with the checkpoint ID printed above)
go run . -mode resume -lineage-id demo \
  -checkpoint-id <checkpoint-id> \
  -resume-value approved
```

## Flags

- `-mode`: `run` or `resume`
- `-lineage-id`: stable identifier used to group checkpoints
- `-db`: SQLite database file path
- `-checkpoint-id`: required for `resume`
- `-resume-value`: value returned by `graph.Interrupt` on resume

## Files

- `examples/graph/nested_interrupt/main.go`
