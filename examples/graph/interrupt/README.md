# Interrupt & Resume Example

This example demonstrates how to build a graph that can interrupt its
execution (e.g., for manual approval) and then resume from a saved
checkpoint.

## Features

- Interrupt execution from a node and surface a value (prompt/payload).
- Save checkpoint automatically on interrupt.
- Resume later with user-provided input.
- In-memory checkpoint saver for easy local testing.

## Prerequisites

- Go 1.21+

## Run

Change into the example directory and run the program. The example
supports multiple modes via flags.

```bash
cd examples/graph/interrupt
go run . -mode run
```

### Modes

- run: Execute the full graph normally (no explicit interrupt handling).
- interrupt: Run and stop at the interrupt point, saving a checkpoint.
- resume: Resume from the latest checkpoint, providing user input via
  the -input flag.
- demo: Demonstrate a complete flow: interrupt -> resume -> list
  checkpoints.

### Examples

Run until interrupt:

```bash
go run . -mode interrupt
```

Resume with approval:

```bash
go run . -mode resume -input yes
```

Run the full demo:

```bash
go run . -mode demo
```

## How it works

- The node `request_approval` calls `graph.Interrupt(ctx, state, "approval",
  payload)`. If there is no resume value, execution returns an interrupt
  error and the executor stores a checkpoint with the interrupt state.
- Later, invoke with a `Command` whose `ResumeMap` contains a value for
  the key `approval`. Execution continues from the next steps.

## Expected output (abridged)

- interrupt mode shows an interrupt event detected and that the execution
  was interrupted with a checkpoint saved.
- resume mode shows the flow continuing and finishing successfully.

## Notes

- This example uses the in-memory checkpoint saver for simplicity. In
  production, use a persistent saver (e.g., SQLite, Postgres).
