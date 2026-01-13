# Ralph Loop Example

This example demonstrates `runner.WithRalphLoop`, a runner-level "outer loop"
that keeps an agent running until a verifiable completion condition is met.

Ralph Loop is useful when an agent tends to stop early because a Large
Language Model (LLM) may *think* it is done, but the task is not actually
complete.

## What it does

- Wraps an `agent.Agent` with Ralph Loop mode.
- Runs the agent repeatedly until it outputs a completion promise:
  `<promise>DONE</promise>`.
- Uses `MaxIterations` as a safety valve to prevent infinite loops.

## How to run

From the repo root:

```bash
cd examples/ralphloop
go run .
```

## Notes

- In real projects, you typically combine a completion promise with an
  objective verifier such as `go test ./...` (exit code must be 0).
- When `MaxIterations` is reached without success, the runner emits an error
  event with error type `stop_agent_error`.

