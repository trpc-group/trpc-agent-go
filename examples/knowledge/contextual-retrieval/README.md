# Contextual Retrieval Example

This example shows how to try **index-time contextual retrieval** with the
existing tRPC-Agent-Go APIs. It is opt-in because contextual retrieval is
corpus-dependent and current benchmark evidence does not establish a general
end-to-end benefit.

Use it when chunks in a business corpus are difficult to understand without
their parent document, then evaluate both variants with representative queries.

## What Changes

The example uses the standard file source to split one local Markdown or text
file. The two index variants are:

- `baseline`: embed the normal chunk text.
- `contextual`: ask a model for a short context, then embed:

  ```text
  Context:
  <generated context>

  --- Original chunk ---
  <normal chunk embedding text>
  ```

Only `Document.EmbeddingText` changes. `Document.Content`, IDs, metadata, and
chunk order stay unchanged, so the Agent receives the original chunk.

```text
local file -> file.Source -> optional contextual source -> Knowledge.Load
                                      |                         |
                                      +-> model + local cache   +-> vector search -> Agent
```

Retrieval is fixed to vector-only mode. This keeps a paired comparison focused
on the dense embedding text rather than silently adding keyword/BM25 behavior.

## Environment

The example uses OpenAI-compatible model and embedding adapters:

```bash
export OPENAI_API_KEY=xxx
export OPENAI_BASE_URL=https://api.openai.com/v1  # optional
export MODEL_NAME=deepseek-v4-flash              # optional Agent model
export CONTEXT_MODEL_NAME=deepseek-v4-flash      # optional context model
export EMBEDDING_MODEL_NAME=text-embedding-3-small
```

`CONTEXT_MODEL_NAME` falls back to `MODEL_NAME`.

## Run a Paired Trial

Run both commands from this directory and change only `-index-variant`.

Baseline:

```bash
go run . \
  -index-variant baseline \
  -input ../exampledata/file/llm.md \
  -query "What are Large Language Models?"
```

Contextual:

```bash
go run . \
  -index-variant contextual \
  -input ../exampledata/file/llm.md \
  -context-cache .context-cache/contexts.json \
  -query "What are Large Language Models?"
```

Keep the answer model, context model, embedding model, input, query set, and
evaluation protocol fixed when comparing the variants.

## Local Context Cache

Context generation adds one model call per unique chunk. The contextual variant
stores successful contexts in a local JSON file and reuses them on later runs.
The cache key includes the fixed prompt version, context model name, parent
document, chunk content, and normal embedding text.

The cache is written atomically with file mode `0600`. It contains text derived
from the source document, so treat it as sensitive and do not commit it. Delete
the cache after changing provider deployments or generation behavior not
represented by the model name.

This cache is intentionally a small, single-process example. A production
integration should choose its own persistence, concurrency, retention, and
monitoring policies.

## Security, Cost, and Adoption

- Context generation sends the full parent document and each chunk to the
  configured model provider. Confirm that this is permitted by your data policy.
- Context generation adds indexing latency and model cost.
- Generated context can be inaccurate; indexing fails rather than silently
  falling back when generation returns an error or empty text.
- Evaluate retrieval quality and downstream answer quality on representative
  business traffic before adoption.

The implementation lives under `internal/contextual` to keep `main.go` focused
on integration. It is example code to copy and adapt, not a public framework API
or a production-ready component.

## Tests

The tests use fake sources and models; they require no API key or network:

```bash
go test ./...
```
