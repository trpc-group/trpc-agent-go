# GraphRAG Viewer

This component starts a small local web UI for inspecting the Apache AGE graph created by the GraphRAG example.

It is intended for local debugging only. The backend serves a static page and exposes read-only graph APIs backed by AGE queries.

## Run

From `examples/knowledge`:

```bash
go run ./features/graphrag/viewer
```

Default URL:

```text
http://127.0.0.1:3012
```

## Configuration

The viewer uses the same AGE database settings as the GraphRAG chat example:

```bash
export AGE_HOST=127.0.0.1
export AGE_PORT=5432
export AGE_USER=root
export AGE_PASSWORD=123
export AGE_DATABASE=contextengine
export AGE_GRAPH_NAME=knowledge_graph
```

`AGE_DSN` can be used to override the individual database fields.

Runtime flags:

```bash
go run ./features/graphrag/viewer \
  -addr=127.0.0.1:3012 \
  -graph=knowledge_graph \
  -node-limit=100 \
  -edge-limit=1000
```

The page also lets you change node and edge limits before loading graph data.
