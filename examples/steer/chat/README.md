# Interactive Steering

Read and write concurrently. Ask for a detailed answer, then type a correction while it streams. Ask for a slow job to see input wait for the tool. /cancel ends the current Run; /quit exits. Input after completion starts a new Run in the same session.

## Run

Use Go 1.24.4 or later. Set `OPENAI_API_KEY` and, for a compatible endpoint,
`OPENAI_BASE_URL`. Model selection is `-model`, then `OPENAI_MODEL`, then
`MODEL_NAME`, then `gpt-4.1-mini`.

From this directory:

```bash
go run . -timeout=30m
```

For the repository's local GLM configuration, source `glm.sh` from the repository
root before changing to this directory. Do not copy credentials into examples.

The Run retains its request ID and root invocation ID. Queued input is accepted
in memory first; its user event confirms consumption. Only `runner.completion`
ends the Run. Tool batches remain complete, and model cancellation requires a
cooperative provider. Explicit cancellation and deadlines still stop the Run.

The terminal input reader is closed on exit. EOF waits for the current Run to finish.

See [all steering examples](../README.md) for the real-provider smoke matrix.
