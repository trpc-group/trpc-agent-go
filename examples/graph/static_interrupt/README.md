# Static Interrupts (Debug Breakpoints)

This example demonstrates **static interrupts** (debug breakpoints) in the
`graph` executor.

Unlike Human-in-the-Loop (HITL) interrupts where you call `graph.Interrupt(...)`
inside a node, **static interrupts require no code inside the node**.
You only attach options when declaring nodes:

- `graph.WithInterruptBefore()` pauses **before** a node executes.
- `graph.WithInterruptAfter()` pauses **after** a node executes.

## What you will see

This workflow has three nodes:

1. `start` runs normally
2. `middle` has both `WithInterruptBefore()` and `WithInterruptAfter()`
3. `end` finishes the workflow

So the program runs three times:

1. Interrupts **before** `middle`
2. Resumes, runs `middle`, then interrupts **after** `middle`
3. Resumes again and completes at `end`

## Run

```bash
go run .
```

Notes:

- This example uses the official in-memory checkpoint saver, so resume happens
  within the same process run.
- For a persistent resume workflow (restart the program later), use a real
  saver such as SQLite or Redis.

