# External Interrupt (Pause Button) Example

This example demonstrates a **first-class external interrupt** for graph
execution.

An external interrupt is a "pause button" that is triggered by code **outside**
the graph. It is useful when you want to:

- Pause before an expensive step (for example, before calling an LLM)
- Save a checkpoint and resume later
- Optionally force a pause with a timeout (cancel running tasks, then resume)

This example uses `graph.WithGraphInterrupt(...)` to:

1. Start a graph run
2. Request a pause from outside the graph
3. Resume from the saved checkpoint

## What you will see

The program runs two demos:

### 1) Planned pause (safe boundary)

- The graph starts at `prepare`
- While `prepare` is running, the program requests an external interrupt
- The executor finishes the current step, then **pauses before** `call_model`
- The program resumes from the checkpoint and completes

### 2) Forced pause (timeout)

- The graph starts at `slow`
- The program requests an external interrupt with a short timeout
- When the timeout fires, running tasks are cancelled and the executor writes a
  resumable checkpoint
- The program resumes and completes

## Run

```bash
go run .
```

Flags:

- `-demo planned|forced|both` (default: `both`)
- `-engine bsp|dag` (default: `bsp`)
- `-model <model-name>` (used by the planned demo when a real model is enabled)
- `-text <user-text>` (the planned demo prompt)

## Model configuration (planned demo)

The planned demo will call a real OpenAI-compatible model **only if**
`OPENAI_API_KEY` is set. Otherwise it falls back to a local stub node so you
can still see the pause/resume behavior without any external dependencies.
