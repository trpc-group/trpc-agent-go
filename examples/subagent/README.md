# Dynamic Subagent Example

This example shows how application code can spawn a background subagent run,
wait for the final result, and optionally persist the run record.

Run with in-memory state:

```bash
go run ./examples/subagent
```

Run with JSON file state:

```bash
go run ./examples/subagent -store ./subagent-runs.json
```

The example uses a deterministic local agent so it does not require an API key.
Real applications normally replace `reportAgent` with an LLM agent or a named
runner agent selected through `SpawnRequest.AgentName`.
