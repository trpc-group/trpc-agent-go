# Keep the Accepted Tool Batch Complete

Job A queues Q2 when it starts. All requested jobs must run exactly once. Every tool result precedes Q2 in the next model request. No model.interrupted event is expected during tool execution.

## Run

Use Go 1.24.4 or later. Set `OPENAI_API_KEY` and, for a compatible endpoint,
`OPENAI_BASE_URL`. Model selection is `-model`, then `OPENAI_MODEL`, then
`MODEL_NAME`, then `gpt-4.1-mini`.

From this directory:

```bash
go run . -check -calls=1
go run . -check
go run . -check -parallel
go run . -check -calls=1 -interrupt=false
```

For the repository's local GLM configuration, source `glm.sh` from the repository
root before changing to this directory. Do not copy credentials into examples.

The Run retains its request ID and root invocation ID. Queued input is accepted
in memory first; its user event confirms consumption. Only `runner.completion`
ends the Run. Tool batches remain complete, and model cancellation requires a
cooperative provider. Explicit cancellation and deadlines still stop the Run.

Automatic checks return a nonzero exit code if the target phase was not reached, the model did not emit the requested tool batch, or an invariant fails. No mock fallback is used.

Callback logs and event-consumer logs can interleave because they run concurrently.
`-check` validates the actual next model request and the event stream ordering.

See [all steering examples](../README.md) for the real-provider smoke matrix.
