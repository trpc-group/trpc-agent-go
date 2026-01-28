# Cancel a Run (Stop a Running Agent) Example

This example answers a very common beginner question:

> “My agent is running — how do I stop it safely?”

In `trpc-agent-go`, **the correct way to stop a running agent is to cancel the
`context.Context` that you passed into `Runner.Run`**.

If you only stop reading the event channel (for example, you `break` your
`for range eventCh` loop), the agent goroutine may keep running and can block
on channel writes.

## What this example demonstrates

- A long-running agent that streams output slowly.
- Two user-friendly stop methods:
  - Press **Enter** to cancel the run.
  - Press **Ctrl+C** to cancel (SIGINT).
- The safe consumer pattern: **cancel, then keep draining events until the
  channel is closed**.

This example does **not** call any external Large Language Model (LLM)
provider. It is safe to run without API keys.

## Run it

```bash
cd examples/cancelrun
go run .
```

You should see streaming output. Press **Enter** (or **Ctrl+C**) to stop.
If you do nothing, it stops automatically after a short timeout.

## See also

- `docs/mkdocs/en/runner.md` (run control guide)
- `examples/managedrunner` (cancel by requestID, detached cancellation)
