# Managed Runner Quickstart: Detached Cancellation and Run Control

This example shows how to run an agent "in the background" and still keep
control over it.

It demonstrates:

- A request identifier (requestID, request identifier) with
  `agent.WithRequestID`
- Detached cancellation with `agent.WithDetachedCancel(true)`
- A maximum runtime with `agent.WithMaxRunDuration`
- Run status with `runner.ManagedRunner.RunStatus`
- Manual cancellation with `runner.ManagedRunner.Cancel`

## What is `context.Context`?

In Go, `context.Context` (often named `ctx`) is a value you pass through your
call stack.

It can carry:

- A cancellation signal (someone calls `cancel()`)
- A deadline (a time limit, also called a timeout)
- Extra values (metadata)

By default, when the parent `ctx` is cancelled, the runner stops the run.

## What does "detached cancellation" mean?

Detached cancellation means:

- Parent cancellation does not stop the run
- Deadlines still stop the run

Runner enforces the earlier (smaller) time limit of:

- The parent `ctx` deadline (if any)
- `MaxRunDuration` (if set)

## Run It

This example does not call any external Large Language Model (LLM) provider.

```bash
cd examples/managedrunner
go run .
```

You will see three short demos:

1. Parent cancellation is ignored, then the run stops by `MaxRunDuration`.
2. A run is cancelled manually by `requestID`.
3. The earlier of parent timeout and `MaxRunDuration` is enforced.
