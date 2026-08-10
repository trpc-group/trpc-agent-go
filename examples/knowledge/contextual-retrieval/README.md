# Contextual Retrieval Example

This example shows how to try **index-time contextual retrieval** with the
existing tRPC-Agent-Go APIs. It is opt-in because contextual retrieval is
corpus-dependent and current benchmark evidence does not establish a general
end-to-end benefit.

Use it when chunks in a business corpus are difficult to understand without
their parent document, then evaluate both variants with representative queries.

## What Changes

The example uses the standard file source to split one local UTF-8 text file;
the file extension selects the registered reader. The two index variants are:

- `baseline`: embed the normal chunk text.
- `contextual`: ask a model for a short context, then embed:

  ```text
  Context:
  <generated context>

  --- Original chunk ---
  <normal chunk embedding text>
  ```

Only `Document.EmbeddingText` changes. The normal framework embedding text
includes structural metadata such as file name, positive chunk index, and
Markdown section when available; the contextual variant retains that exact
base text after the generated context. `Document.Content`, IDs, metadata, and
chunk order stay unchanged, so the Agent receives the original chunk.

```text
local file -> file.Source -> optional contextual source -> Knowledge.Load
                                      |                         |
                                      +-> model + local cache   +-> vector search -> Agent
```

Retrieval is fixed to vector-only mode. This keeps a paired comparison focused
on the dense embedding text rather than silently adding keyword/BM25 behavior.

### Relationship to the benchmark experiment

This example uses the same core A/B topology as the contextual-retrieval work
in [trpc-agent-go-benchmark#20](https://github.com/trpc-group/trpc-agent-go-benchmark/pull/20):
baseline embeds the framework's normal text, while contextual embeds generated
context plus that same base text. It is not a reproduction of that benchmark.
The prompt, models, corpus, cache, retrieval setup, and evaluation protocol may
differ, so results from the example and benchmark are not directly comparable.

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

On a cold cache, context generation is sequential and adds one model call per
unique cache miss. Every call sends the full parent document again with one
chunk, so total input grows approximately with `parent bytes × chunk count`, in
addition to the chunk and prompt bytes. Before making any calls, the example
estimates the cumulative prompt bytes for unique cache misses and fails closed
above an internal 4 MiB limit. This byte guard is a bounded-example safeguard,
not a provider token or price estimate. With the current bundled 8,966-byte
input and 500/50 chunk settings, a cold run produces 29 calls and roughly 30×
raw input amplification before provider framing.

The contextual variant stores only successful contexts that ended with the
model finish reason `stop` in a local JSON file and reuses them on later runs.
The cache key covers the prompt and finish policy, context model name, a hashed
normalized endpoint identity, fixed generation parameters, parent document,
chunk content, and normal framework embedding text. The baseline variant does
not read or write this cache.

New cache snapshots are written atomically through a temporary file with mode
`0600`. Opening an existing cache does not change its permissions. The cache
contains text derived from the source document, so treat it as sensitive and do
not commit it. The caller is responsible for ownership and permissions of an
existing cache path and its directory; this example does not defend against a
local actor that can replace them.

Each successful cache miss rewrites and syncs the snapshot immediately, so an
expensive context already generated survives a later provider error or canceled
run. This favors recoverability over write throughput for the bounded example.
An unsupported cache schema version is rejected; move or delete that cache
explicitly before regenerating rather than letting the example overwrite an
unknown format.

This cache is intentionally a small, single-process example. A production
integration should choose its own persistence, concurrency, retention, and
monitoring policies.

## Security, Cost, and Adoption

- Context generation sends the full parent document and each chunk to the
  configured model provider. Confirm that this is permitted by your data policy.
- Parent and chunk strings are JSON-encoded to avoid ambiguous delimiter syntax.
  JSON is not a prompt-injection security boundary; model-visible document text
  can still influence generation.
- Context generation adds indexing latency and model cost. Cold generation is
  intentionally sequential in this bounded example.
- Generated context can be inaccurate; indexing fails rather than silently
  falling back when generation returns an error, empty text, missing finish
  reason, or any finish reason other than `stop`.
- Evaluate retrieval quality and downstream answer quality on representative
  business traffic before adoption.

The implementation lives under `internal/contextual` to keep `main.go` focused
on integration. It is example code to copy and adapt, not a public framework API
or a production-ready component. Its scope is deliberately one local file, an
in-memory vector store, one process, and sequential context generation.

## Tests

The tests use fake sources and models; they require no API key or network:

```bash
go test ./...
```
