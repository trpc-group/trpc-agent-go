# Cancellation and tool execution

When a run is cancelled, the framework checks cancellation before admitting a
tool, after waiting for tool capacity, and after callbacks or permissions that
may have waited. Callable and streaming tool entry points also reject an
already-cancelled context. New model steps and provider requests do not start
once their context is cancelled.

This changes the earlier no-retry behavior, which could invoke a tool with an
already-cancelled context. Tool cleanup must not depend on starting a fresh tool
after cancellation. After-tool callbacks still run when cancellation is observed
after before-tool callbacks, so they can release resources already acquired.

A cancellation check is not atomic with an external operation. Tools must still
propagate the context to I/O, and side-effecting operations may require idempotency
or an explicit outcome check. Already-running goroutines cannot be forcibly
terminated by the framework.

## Optional live validation

The normal test suite does not call a paid provider. To run the cancellation
checks against an OpenAI-compatible endpoint, set `OPENAI_BASE_URL`,
`OPENAI_API_KEY`, and `MODEL_NAME` in the environment, then run:

```sh
TRPC_CANCEL_LIVE=1 go test ./runner -run '^TestCancellationLive_' -v -count=1 -timeout=5m
```

The tools only update local test counters. The checks distinguish stopping the
client output from stopping remote computation; they do not assert server-side
termination, rollback, or billing behavior. Do not include credentials in test
logs or pull request descriptions.
