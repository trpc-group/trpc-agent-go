# Ralph Loop Example

This example demonstrates `planner/ralphloop`, a planner-based "Hard Ralph"
mode for `LLMAgent` (Large Language Model Agent).

Ralph Loop is useful when an agent tends to stop early because a Large
Language Model (LLM) may *think* it is done, but the task is not actually
complete.

## What it does

- Configures an `LLMAgent` with a RalphLoop Planner.
- Uses a local fake model that "finishes" too early on the first call.
- Forces the LLM flow to continue until the assistant outputs
  `<promise>DONE</promise>`.
- Uses `MaxIterations` and `WithMaxLLMCalls` as safety valves.

## How to run

From the repo root:

```bash
cd examples/ralphloop
go run .
```

## Notes

- In real projects, you usually use a real model provider instead of the fake
  model used here.
- If you need objective verification (for example, tests must pass), implement
  a custom `ralphloop.Verifier` and pass it via `ralphloop.Config.Verifiers`.
