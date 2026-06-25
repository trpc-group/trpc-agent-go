# Code Context Engine (`code_search`)

Ask a repository question and let an agent answer it with AST-aware code search instead of grepping blindly. This example showcases the framework's [`code_search`](../../../../knowledge/tool/codesearchtool.go) tool — semantic vector search plus metadata / literal filtering over a [repo source](../../sources) — in two shapes:

- **`mcp/`** — wraps `code_search` as an MCP server, so any MCP client (Cursor, Augment, or another trpc-agent-go runner) can consume the exact same AST-backed code search pipeline.
- **`comparison/`** — A/B-tests a local agent (using our `code_search` via the MCP server) against an Augment agent (using Augment's hosted `augment_code_search`) on the same repository questions, and writes a side-by-side Markdown report.

The indexed repository is `trpc-agent-go` itself (`.go` + `.md`, skipping generated and `_test.go` files).

## What it shows

- How `code_search` turns a repo source into an agent-callable tool over a plain vector store (no graph database required).
- How to expose that tool over MCP so it is reusable outside this process.
- How an AST-aware local code search compares against a hosted code-search engine on real "how does X work in this repo" questions.

For the conceptual overview (where `code_search` sits in the ingest → parse → retrieve pipeline, its options, and how it relates to the GraphRAG `code_graph_*` tools), see the **Code RAG** page in the knowledge docs: [zh](../../../../docs/mkdocs/zh/knowledge/code-rag.md) · [en](../../../../docs/mkdocs/en/knowledge/code-rag.md).

## Layout

| Path | What it is |
|------|------------|
| `mcp/server.go` | MCP server exposing the local `code_search` tool over `streamable_http`. Owns the knowledge base (repo source + embedder + vector store) and its ingestion lifecycle. |
| `comparison/main.go` | Runs the comparison cases through one or both agents and writes per-case reports. |
| `comparison/local_agent.go` | LLM agent whose only tool is the remote `code_search` from our MCP server. |
| `comparison/augment_agent.go` | LLM agent whose only tool is Augment's hosted `augment_code_search` (via MCP). |

## Prerequisites

```bash
# Required: model + embedding provider for the agent and the knowledge base.
export OPENAI_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=sk-xxxx

# Optional: override the agent chat model (example default: gpt-5.4).
export MODEL_NAME=gpt-5.4
# Optional: override the embedding model used by the MCP server.
export EMBEDDING_MODEL_NAME=text-embedding-3-small
# Optional: vector store backend for the MCP server (default: inmemory).
export VECTOR_STORE_TYPE=inmemory

# Only for the augment comparison (-mode=augment|both):
export AUGMENT_CONTEXT_ENGINE_API_KEY=...
```

`VECTOR_STORE_TYPE` accepts `inmemory | pgvector | sqlitevec | tcvector | elasticsearch | milvus`. Non-memory backends read their own connection env vars (e.g. `PGVECTOR_*`); see [examples/knowledge/util.go](../../util.go).

All commands below run from this directory:

```bash
cd examples/knowledge/features/code_context_engine
```

## 1. Start the `code_search` MCP server

The server clones + indexes the repository on startup, then serves `code_search` over MCP. With `inmemory` the index lives in process, so keep it running while you use the comparison.

```bash
go run ./mcp
# -> MCP server listening on 127.0.0.1:3001/mcp
```

Useful flags:

| Flag | Default | Purpose |
|------|---------|---------|
| `-addr` | `127.0.0.1:3001` | HTTP listen address |
| `-path` | `/mcp` | HTTP path prefix for the MCP endpoint |
| `-store` | `$VECTOR_STORE_TYPE`, else `inmemory` | Vector store type; defaults to the `VECTOR_STORE_TYPE` env var, falling back to `inmemory` when it is unset |
| `-skip-load` | `false` | Reuse existing vector-store data, skip ingestion (only meaningful for persistent stores) |
| `-truncate-old` | `false` | Recreate the vector store before ingestion (deletes all existing documents) |

## 2. Run the comparison

In another terminal (with the MCP server still running for `local`/`both`):

```bash
# Local code_search only (needs the MCP server from step 1):
go run ./comparison -mode=local

# Augment only (needs AUGMENT_CONTEXT_ENGINE_API_KEY):
go run ./comparison -mode=augment

# Both, side by side:
go run ./comparison -mode=both
```

Useful flags:

| Flag | Default | Purpose |
|------|---------|---------|
| `-mode` | `both` | Which agent(s) to run: `local` \| `augment` \| `both` |
| `-output-dir` | `output/code_context_engine` | Where per-case Markdown reports are written |
| `-local-mcp-url` | `http://localhost:3001/mcp` | URL of the local code-search MCP server |
| `-local-mcp-tool` | `code_search` | Name of the MCP tool to call on that server |

## Output

For each case, a Markdown report is written under `-output-dir`, containing the final answer plus the full trace of tool calls and tool results for each agent — so you can see exactly which `code_search` / `augment_code_search` queries and filters each agent issued, and how the answers differ.

## Reuse the MCP server elsewhere

Because `mcp/server.go` exposes `code_search` over standard MCP, you can point any MCP client at `http://localhost:3001/mcp` and get the same AST-backed code search — the comparison's local agent is just one such client. This is the recommended way to plug trpc-agent-go's code search into an external IDE assistant or a different runner.

## Related

- **Code RAG** docs: [zh](../../../../docs/mkdocs/zh/knowledge/code-rag.md) · [en](../../../../docs/mkdocs/en/knowledge/code-rag.md)
- GraphRAG (graph traversal over the same AST entities): [../graphrag](../graphrag)
- Repo source & AST readers: [../../sources](../../sources)
