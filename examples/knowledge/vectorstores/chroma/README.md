# Chroma Vector Store Example

Demonstrates using Chroma as a `vectorstore.VectorStore` in a full knowledge
pipeline: a file source is chunked, embedded with the OpenAI embedder, loaded
into Chroma, and searched through an LLM agent with the knowledge search tool.

The storage package calls the Chroma v2 HTTP API directly and does not depend
on a third-party Chroma Go SDK.

See the [ChromaDB vector store documentation](../../../../docs/mkdocs/en/knowledge/vectorstore/chromadb.md)
for the complete behavior and configuration reference.

## Prerequisites

1. Start Chroma 1.5.3 or newer:

```bash
docker run -d --name chroma -p 8000:8000 chromadb/chroma:1.5.3
```

2. Set environment variables:

```bash
export OPENAI_API_KEY=sk-xxxx
export OPENAI_BASE_URL=xxx          # optional, custom OpenAI-compatible endpoint
export MODEL_NAME=deepseek-v4-flash # optional
export CHROMA_URL=http://localhost:8000
export CHROMA_COLLECTION=trpc_example
```

For Chroma Cloud, set an API key instead of a local URL:

```bash
export CHROMA_API_KEY=xxxx          # enables chroma.WithAPIKey
export CHROMA_TENANT=xxxx           # optional, inferred from Cloud identity when omitted
export CHROMA_DATABASE=xxxx         # optional, inferred from Cloud identity when omitted
```

The example defaults to `http://localhost:8000` and `trpc_example`. The
`chroma.New` API itself does not default to `http://localhost:8000`.

The default index dimension (1536) matches the OpenAI `text-embedding-3-small`
embedder. When you override the embedding model with a different dimension,
pass `chroma.WithIndexDimension` accordingly.

For authentication, use `chroma.WithAPIKey`, `chroma.WithBearerToken`, or
`chroma.WithHeaders`. These options only send request headers and do not enable
server-side authentication. Self-hosted Chroma 1.0+ has no built-in
authentication. For Chroma Cloud, `WithAPIKey` infers tenant and database from
identity when they are omitted.

## Run

```bash
cd examples/knowledge/vectorstores/chroma
go run main.go
```

The program loads `exampledata/file/llm.md` into Chroma, then asks the LLM
agent a question that is answered through the knowledge search tool.

## Keyword and hybrid search

Prefer Chroma Cloud for keyword and hybrid search. Self-hosted Chroma does not
provide server-side BM25, and that capability is not expected soon.

`WithSparseSearch(embedder)` enables the native `/search` path for keyword and
hybrid retrieval. The embedder generates sparse vectors for both documents and
queries. On a new collection, this option also writes a sparse vector index
(default metadata key `sparse_embedding`). Use `WithSparseSearchKey` and
`WithHybridWeights` to override the field and RRF weights. The server must
implement `/search`; configured sparse search errors do not silently fall back.
