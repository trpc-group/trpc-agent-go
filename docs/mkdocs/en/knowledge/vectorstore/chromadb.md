# ChromaDB

> **Example Code**: [examples/knowledge/vectorstores/chroma](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/chroma)

[Chroma](https://docs.trychroma.com/) is an open-source vector database. The `knowledge/vectorstore/chroma` package implements `vectorstore.VectorStore` against the Chroma v2 REST API and requires Chroma 1.5.3 or newer.

Keyword and hybrid search rely on the `/search` API provided by [Chroma Cloud](https://docs.trychroma.com/cloud/getting-started). Self-hosted servers support vector and filter search only.

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    chroma "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/chroma"
)

chromaVS, err := chroma.New(ctx,
    chroma.WithBaseURL("http://localhost:8000"),
    chroma.WithCollection("my_documents"),
    chroma.WithIndexDimension(1536), // Must match the embedding model
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(chromaVS),
    knowledge.WithEmbedder(embedder),
)
```

## Chroma Cloud Configuration

```go
chromaVS, err := chroma.New(ctx,
    chroma.WithBaseURL("https://api.trychroma.com"),
    chroma.WithAPIKey("your-api-key"),
    chroma.WithCollection("my_documents"),
    chroma.WithIndexDimension(1536),
)
```

`WithAPIKey` sends `X-Chroma-Token`. Tenant and database are resolved from the Cloud identity when omitted.

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithBaseURL(url)` | Chroma HTTP address. Required without a named instance. | - |
| `WithInstanceName(name)` | Use a client registered with `storage.RegisterChromaInstance` | - |
| `WithTenant(tenant)` / `WithDatabase(database)` | Tenant and database. Empty uses the server default, or the Cloud identity with `WithAPIKey`. | Server default |
| `WithAPIKey(key)` | Send `X-Chroma-Token` | - |
| `WithBearerToken(token)` | Send `Authorization: Bearer` | - |
| `WithHeaders(headers)` | Extra request headers. Custom auth headers require tenant and database. | - |

> Authentication options only add request headers; self-hosted Chroma 1.0+ has no built-in auth.

### Collection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithCollection(name)` | Collection name | Required |
| `WithIndexDimension(dim)` | Vector dimension (must match the embedding model) | `1536` |
| `WithAutoCreateCollection(enable)` | Create the collection when it is missing | `true` |
| `WithMaxResults(n)` | Default search limit | `10` |
| `WithMaxRequestRecords(n)` | Maximum records in one Chroma request. Vector Query and `/search` results are capped at this size; Get operations page beyond it. | `300` |

Collections must use the cosine metric: new collections are created as cosine HNSW, and an existing non-cosine collection fails at startup.

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithSparseSearch(embedder...)` | Enable keyword and hybrid search. The embedder is optional: omitting it uses the built-in Cloud SPLADE embedder, which requires `WithAPIKey`. | Disabled |
| `WithSparseSearchKey(key)` | Metadata key that stores sparse vectors | `"sparse_embedding"` |
| `WithHybridWeights(dense, sparse)` | Dense/sparse RRF weights; normalized to sum to 1 | `0.5, 0.5` |

### Built-in Cloud SPLADE Embedder

`WithSparseSearch()` without an embedder uses `NewCloudSpladeEmbedder`, which encodes documents and queries through Chroma Cloud's hosted SPLADE embedding API (`prithivida/Splade_PP_en_v1`) with the key from `WithAPIKey`:

```go
vs, err := chroma.New(ctx,
    chroma.WithBaseURL("https://api.trychroma.com"),
    chroma.WithAPIKey(apiKey),
    chroma.WithCollection("docs"),
    chroma.WithIndexDimension(1536),
    chroma.WithSparseSearch(), // built-in Cloud SPLADE embedder
)
```

- Every write of a non-empty document and every keyword or hybrid search performs one hosted embedding call, subject to the account's rate limits.
- Auto-created collections declare the `chroma-cloud-splade` embedding function in their schema, mirroring the official Python, TypeScript, and Rust clients, so clients reading the schema can reconstruct a compatible embedding function for the same collection.
- The model is English-focused. For other languages (including Chinese), implement the `SparseEmbedder` interface and pass it to `WithSparseSearch`.
- `WithSpladeBaseURL` and `WithSpladeModel` customize the embedding service address and model when constructing `NewCloudSpladeEmbedder` explicitly; `WithSpladeHTTPClient` customizes the HTTP client (transport, proxy, TLS, timeouts, tracing) used for embedding requests.

### Custom Sparse Embedders

For other languages or self-managed sparse models, implement the `SparseEmbedder` interface (e.g. dictionary segmentation or BGE-M3 sparse output) and pass it to `WithSparseSearch`. Implementations must be safe for concurrent use, both encodings must share one vector space, and sparse vector indices must be strictly increasing and fit in int32. Once a collection is written with one encoder, keep using the same encoder for queries; switching encoders requires rewriting the collection.

## Search Modes

| Mode | Support | Description |
|------|---------|-------------|
| Vector | ✅ | Dense cosine similarity via Chroma `Query` |
| Filter | ✅ | IDs, metadata, or document text via `Get` |
| Keyword | ⚠️ | Sparse KNN via Cloud `/search`. Requires `WithSparseSearch`. |
| Hybrid | ⚠️ | Dense+sparse weighted RRF. Falls back to Vector when sparse search is not configured. |

Notes:

- `WithSparseSearch` writes a sparse index only on **newly created** collections. An existing collection must already have one; Chroma cannot add it later.
- Hybrid fuses both branches with `score = wd·k/(k+dense_rank) + ws·k/(k+sparse_rank)`, `k = 60` by default. A rank missing from one branch uses the position after that branch's candidate window.
- Once sparse search is configured, a `/search` failure returns an error; there is no silent fallback to dense search.

## Metadata And Filters

`name`, `created_at`, `updated_at`, and `_json` are reserved metadata keys; Add and Update reject documents that use them. Nested metadata values are stored in `_json` for round-trip but cannot be filtered. The configured sparse key is adapter-owned and must not be written directly.

| Universal operator | Chroma operator |
|--------------------|-----------------|
| eq / ne / gt / gte / lt / lte | `$eq` / `$ne` / `$gt` / `$gte` / `$lt` / `$lte` |
| in / not in | `$in` / `$nin` |
| and / or | `$and` / `$or` |
| like / not like (`content` field only) | `$contains` / `$not_contains` |

`between` is not supported, and OR across IDs, metadata, and document text is not supported.

## Behavioral Notes

- Add is upsert. Update only replaces an existing document and keeps the previous vector when the new embedding is empty.
- Add and Update replace the complete metadata set (a non-atomic read-then-write).
- `UpdateByFilter` fixes its matched ID set before writing and rejects more than 100,000 matches; tune with `WithMaxUpdateRecords`.
- Filter search with no selector returns an error instead of scanning the whole collection.
- DeleteAll requires `vectorstore.WithDeleteAll(true)` and cannot be combined with other selectors:

```go
store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
```
