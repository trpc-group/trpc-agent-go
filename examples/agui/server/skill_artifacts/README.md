# AG-UI Server: `skill_run` Artifacts

This example demonstrates how `skill_run` can persist output files as **artifacts** and how the AG-UI server surfaces them to the frontend as a replayable:

- `CustomEvent("tool.artifacts")`

The event payload includes:

- `toolCallId`: the tool call that produced the artifacts
- `artifacts[*].ref`: stable `artifact://name@version` references

## Run

From the repo root:

```bash
cd examples/agui
go run ./server/skill_artifacts
```

In another terminal, run the raw client to inspect events:

```bash
cd examples/agui
go run ./client/raw --endpoint http://127.0.0.1:8080/agui
```

Type any prompt and press Enter. You should see:

- `TOOL_CALL_*` events for `skill_run`
- a `CUSTOM_EVENT` named `tool.artifacts` containing `artifact://...@ver` refs

## What the server does

- Uses an in-memory Artifact service (no external dependencies).
- Uses a scripted model that always calls `skill_run` once.
- The demo skill is under [`skills/artifact_demo`](skills/artifact_demo).

