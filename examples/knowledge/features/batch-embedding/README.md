# Batch Embedding Example

This example shows what `knowledge.WithEmbeddingBatchSize` actually changes: the
number of embedding requests a load issues. It indexes the same file twice, once
on the default per-document path and once with batching, and prints the request
count of each run.

## What it demonstrates

Batching is opt-in and only takes effect when the configured embedder implements
the optional `embedder.BatchEmbedder` interface. Loading then groups documents
into multi-input requests instead of sending one request per document.

Request counts are not visible from the outside, so the example wraps the
OpenAI-compatible embedder in a counting decorator:

- `countingEmbedder` implements `embedder.BatchEmbedder` and counts both
  single-document and batch calls. Because it keeps the interface, loading
  discovers the batch capability exactly as it would for the wrapped embedder.
- `perDocumentEmbedder` counts the same way but does not implement
  `embedder.BatchEmbedder`. It is used by `-show-fallback` to demonstrate that
  requesting a batch size does not force batching.

Each run uses a fresh in-memory vector store, so no run sees another run's
documents.

## Prerequisites

The example calls a real OpenAI-compatible embedding endpoint; there is no
offline mode.

```bash
export OPENAI_API_KEY=xxx
export OPENAI_BASE_URL=https://api.openai.com/v1   # optional
export EMBEDDING_MODEL_NAME=text-embedding-3-small # optional
```

Each run embeds the whole input file, so a single invocation embeds the corpus
twice, or three times with `-show-fallback`.

## Run

```bash
cd examples/knowledge/features/batch-embedding
go run .
```

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `-batch-size` | `8` | Maximum documents per embedding request; must be at least 2 |
| `-input` | `exampledata/file/llm.md` | Local file to index |
| `-show-fallback` | `false` | Add a run with an embedder that has no batch support |

```bash
go run . -batch-size 4
go run . -input /path/to/document.md
go run . -show-fallback
```

## Reading the output

The default input splits into 12 documents, so the comparison looks like this
(framework log lines omitted, elapsed times depend on the provider):

```text
variant           documents  per-document requests  batch requests  total requests  elapsed
per-document      12         12                     0               12              2.884s
batched (size 8)  12         0                      2               2               621ms

Embedding requests for 12 documents: 12 without batching, 2 with batching (ceil(12/8) = 2).
```

Both runs embed the same 12 documents; only the number of requests carrying them
differs. For one source with `N` documents and batch size `B`, the batched run
issues `ceil(N/B)` requests, here `ceil(12/8) = 2` requests of 8 and 4 documents.

The example does not compare vectors or search results. Batching sends the same
texts to the same model, so it is not expected to change what is indexed, and a
retrieval comparison would only add provider noise.

Elapsed time is printed because it is the reason most callers are interested in
batching, but it is a single sample from one run against one provider. It is not
a benchmark, and it should not be read as a guaranteed speedup: the gain depends
on how the provider handles multi-input requests.

With `-show-fallback`, the third run keeps one request per document even though
it was given the same batch size, and loading logs that the configured batch
size was ignored.

## Limits

- Batches never cross source boundaries. With several sources the total is
  `sum(ceil(Ni/B))`, not `ceil(sum(Ni)/B)`.
- Source synchronization loads document by document, so a batch size configured
  there has no effect.
- A batch size of `1` or less keeps the per-document path. The example rejects
  such a value so that its two variants stay distinguishable.
- Providers limit inputs, tokens, and payload size per request. The framework
  does not split a batch to satisfy those limits, so choose `B` for the model
  and document sizes in use. A rejected batch stores none of its documents and
  fails the load.
