# Graph Progress AG-UI Server

Minimal `GraphAgent` example where each node emits a run-scoped AG-UI progress event through `aguirunner.RunFromContext(ctx)`.

Run from `examples/agui`:

```bash
go run ./server/graph_progress
```

Inspect the SSE stream:

```bash
curl -N http://127.0.0.1:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{"threadId":"graph-progress-demo","runId":"run-1","messages":[{"role":"user","content":"Prepare a launch checklist"}]}'
```

Expected custom events:

```text
CUSTOM(name="graph.node.progress", value.node="intake", value.progress=20)
CUSTOM(name="graph.node.progress", value.node="outline", value.progress=40)
CUSTOM(name="graph.node.progress", value.node="draft", value.progress=60)
CUSTOM(name="graph.node.progress", value.node="review", value.progress=80)
CUSTOM(name="graph.node.progress", value.node="finish", value.progress=100)
```
