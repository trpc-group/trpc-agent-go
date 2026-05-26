# Agent Task Run Example

This example shows how application code can start a background task run,
wait for the final result, and optionally persist the run record.

Run with in-memory state:

```bash
cd examples/taskrun
go run .
```

Run with JSON file state:

```bash
cd examples/taskrun
go run . -store ./task-runs.json
```

The example uses a deterministic local agent so it does not require an API key.
Real applications normally replace `reportAgent` with an LLM agent or a named
runner agent selected through `SpawnRequest.AgentName`.
