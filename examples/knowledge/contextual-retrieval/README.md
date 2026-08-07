# Contextual Retrieval Example

This example shows how to try **index-time contextual retrieval** with the
existing tRPC-Agent-Go public APIs. It is intentionally opt-in and lives in an
example rather than the framework defaults: current benchmark evidence is
mixed and does not establish a general end-to-end benefit for every corpus.

Use this example when a business corpus contains chunks that are hard to
understand without their parent document and you want to evaluate the method
with your own queries and acceptance metrics.

## What It Does

The example wraps a normal local file source and prepares one of two index
variants:

- `baseline`: embeds the chunk's existing `EmbeddingText`, or its `Content`
  when no specialized embedding text exists.
- `contextual`: asks a model for a short description that situates the chunk
  inside its parent document, then embeds:

  ```text
  Context:
  <generated context>

  --- Original chunk ---
  <baseline embedding text>
  ```

Only `Document.EmbeddingText` changes. `Document.Content`, metadata, and chunk
order remain unchanged, so the knowledge search tool and Agent still receive
the original chunk rather than the generated context. The example also fixes
retrieval to vector-only mode, so a paired trial isolates the dense embedding
text change instead of silently adding contextual keyword/BM25 behavior. Both
the context generator and answer Agent use temperature `0`.

```text
local .md/.txt file
        |
        v
standard file.Source (read + split)
        |
        +---------------- baseline ----------------+
        |                                           |
        +--> parent resolver --> context model --> cache
                                      |             |
                                      +-- contextual+
                                                    |
                                                    v
                                           Document.EmbeddingText
                                                    |
                                                    v
                                             Knowledge.Load
                                                    |
                                                    v
                                      original Document.Content to Agent
```

No framework API changes are required. The example uses the existing
`source.Source`, `model.Model`, and `document.Document.EmbeddingText`
contracts.

## Supported Inputs

This first version supports local UTF-8 `.md`, `.markdown`, `.txt`, and `.text`
files. After the standard source reads and chunks a file, the wrapper reads it
once more as the parent snapshot and reuses that snapshot across its chunks.

PDF, URL, OCR, DOCX, and directory sources are deliberately out of scope. For
those sources, the raw input bytes are not necessarily the extracted parent
text, so a business integration should provide a resolver that preserves the
reader's actual parent-document representation.

## Environment

The Agent, context generator, and embedder use OpenAI-compatible adapters:

```bash
export OPENAI_API_KEY=xxx
export OPENAI_BASE_URL=https://api.openai.com/v1  # optional
export MODEL_NAME=deepseek-v4-flash              # optional Agent model
export CONTEXT_MODEL_NAME=deepseek-v4-flash      # optional context model
export CONTEXT_PROVIDER_ID=staging-openai-gateway # optional deployment ID
export EMBEDDING_MODEL_NAME=text-embedding-3-small
```

`CONTEXT_MODEL_NAME` falls back to `MODEL_NAME`. `CONTEXT_PROVIDER_ID` lets two
deployments that expose the same model name keep separate caches; the value is
hashed before storage and must not contain a secret. When omitted, a hash of
`OPENAI_BASE_URL` (or the default OpenAI endpoint) is used. The baseline variant
does not initialize or call the context model.

## Run a Paired Trial

Run commands from this directory. Keep all flags except `-index-variant`
identical when comparing the two variants.

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
  -context-model "$CONTEXT_MODEL_NAME" \
  -context-provider-id "$CONTEXT_PROVIDER_ID" \
  -context-cache .context-cache/contexts.jsonl \
  -context-workers 4 \
  -query "What are Large Language Models?"
```

Multiple input files may be supplied as a comma-separated list. Use the same
answer model, embedding model, chunk size, chunk overlap, vector store,
retrieval settings, query set, and evaluation protocol for both variants.

### Reuse Only Verified Cached Contexts

After a successful contextual run, require complete cache coverage and make no
context-model calls:

```bash
go run . \
  -index-variant contextual \
  -input ../exampledata/file/llm.md \
  -context-model "$CONTEXT_MODEL_NAME" \
  -context-provider-id "$CONTEXT_PROVIDER_ID" \
  -context-cache .context-cache/contexts.jsonl \
  -context-cache-only
```

The model name and provider deployment identity must match the cache-populating
run because both are part of the cache key. A missing entry, empty context,
corrupted hash, provider error, or canceled request stops indexing; the example
never silently falls back to raw chunk embedding.

## Cache and Index Identity

The append-only JSONL cache key includes:

- context provider identity;
- prompt version;
- embedding-text format version;
- SHA-256 of the parent document;
- SHA-256 of the original chunk.

Each record also stores and validates the context SHA-256. Only successful,
non-empty contexts are appended. The cache file is created with mode `0600`.

The source metadata includes the index variant, method version, provider
identity, prompt version, embedding format version, and an ordered context-set
digest. Source synchronization therefore treats a provider, prompt, input, or
generated-context change as a new index identity. This example accepts a full
source refresh instead of trying to optimize per-chunk context updates.

## Persistent Vector Stores

`inmemory` is the safe default. For a persistent vector store,
`-index-namespace` is required. The example appends `_baseline` or
`_contextual` and maps the result to the store's table, collection, or index
setting:

```bash
go run . \
  -index-variant contextual \
  -vectorstore pgvector \
  -index-namespace myapp_context_trial
```

The example does not drop or recreate an existing user index. The selected
dedicated namespace is synchronized by `Knowledge.Load`, so do not point it at
a table, collection, or index shared with production data. Configure the
remaining store-specific connection environment variables described in the
parent [Knowledge examples README](../README.md).

## Security and Cost Notes

- Context generation sends both the full parent text and the chunk to the
  configured model provider. Confirm that this is allowed by your data policy.
- The local cache contains generated text derived from the source documents.
  Treat it as sensitive data and do not commit it.
- Context generation adds model calls, latency, and cost during indexing.
- The contextualization layer does not log parent text, chunk text, generated
  context, cache contents, API keys, or provider payloads. The normal Agent demo
  still prints the query and retrieved original `Document.Content`; treat the
  terminal output accordingly.
- Model-produced context can be inaccurate. Evaluate retrieval quality and
  downstream answer quality on representative business traffic before use.

## Scope and Adoption Boundary

This is a maintained integration example, not a framework-wide recommendation
or proof that contextual retrieval improves every workload. In particular, it
does not implement contextual BM25, hybrid search, reranking, parent retrieval,
or provider-native grouped contextual embeddings / Late Chunking. It fixes the
retrieval path to vector-only rather than demonstrating hybrid search.

If a corpus-specific trial demonstrates a material benefit, the example-local
`contextProvider`, `parentResolver`, cache, and source wrapper provide explicit
seams for a business implementation. A future framework API should be proposed
only after repeated use reveals a stable contract shared by multiple callers.

## Tests

The unit tests use fake sources, resolvers, providers, and models; they require
no API key or network access:

```bash
go test .
```
