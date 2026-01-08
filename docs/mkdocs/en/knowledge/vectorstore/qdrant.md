# Qdrant

> **Example Code**: [examples/knowledge/vectorstores/qdrant](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/qdrant)

[Qdrant](https://qdrant.tech/) is a high-performance vector database with advanced filtering capabilities, supporting cloud and local deployment.

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorqdrant "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/qdrant"
)

// Local Qdrant instance (default: localhost:6334)
qdrantVS, err := vectorqdrant.New(ctx)
if err != nil {
    // Handle error
}

// Custom configuration
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("qdrant.example.com"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),  // Must match embedding model
)

kb := knowledge.New(
    knowledge.WithVectorStore(qdrantVS),
    knowledge.WithEmbedder(embedder),
)
```

## Qdrant Cloud Configuration

```go
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("xyz-abc.cloud.qdrant.io"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithAPIKey("your-api-key"),
    vectorqdrant.WithTLS(true),  // Required for Qdrant Cloud
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),
)
```

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithHost(host)` | Qdrant server hostname | `"localhost"` |
| `WithPort(port)` | Qdrant gRPC port (1-65535) | `6334` |
| `WithAPIKey(key)` | Qdrant Cloud authentication API key | - |
| `WithTLS(enabled)` | Enable TLS (required for Qdrant Cloud) | `false` |
| `WithClient(client)` | Use pre-created client (from storage module) | - |

### Collection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithCollectionName(name)` | Collection name | `"trpc_agent_documents"` |
| `WithDimension(dim)` | Vector dimension (must match embedding model) | `1536` |
| `WithDistance(d)` | Distance metric (Cosine, Euclid, Dot, Manhattan) | `DistanceCosine` |

### Index Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithHNSWConfig(m, efConstruct)` | HNSW index parameters (higher = better recall, more memory) | `16, 128` |
| `WithOnDiskVectors(enabled)` | Store vectors on disk (for large datasets) | `false` |
| `WithOnDiskPayload(enabled)` | Store payload on disk | `false` |

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(max)` | Default number of search results | `10` |
| `WithBM25(enabled)` | Enable BM25 sparse vectors for hybrid/keyword retrieval | `false` |
| `WithPrefetchMultiplier(n)` | Prefetch multiplier for hybrid retrieval fusion | `2` |

### Retry Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxRetries(n)` | Maximum retry count for transient gRPC errors | `3` |
| `WithBaseRetryDelay(d)` | Initial retry delay | `100ms` |
| `WithMaxRetryDelay(d)` | Maximum retry delay | `5s` |

### Other Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithLogger(logger)` | Set logger | - |

## BM25 Hybrid Retrieval

Qdrant supports hybrid retrieval, combining dense vector similarity and BM25 keyword matching, using Reciprocal Rank Fusion (RRF) for result fusion:

```go
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("localhost"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),
    vectorqdrant.WithBM25(true),  // Enable BM25 hybrid retrieval
)
```

With BM25 enabled, the vector store creates collections containing both dense and sparse vectors. Supports the following search modes:

- **Vector Retrieval** (default): Dense vector similarity search
- **Keyword Retrieval**: BM25 sparse vector search (requires `WithBM25(true)`)
- **Hybrid Retrieval**: Fuse dense and sparse results using RRF (requires `WithBM25(true)`)
- **Filter Retrieval**: Metadata-only filtering, no vector similarity

> **Important BM25 Collection Notes:**
>
> - **Collection Compatibility**: Collections with BM25 enabled and disabled have different vector configurations. You cannot create a `WithBM25(true)` vector store on an existing non-BM25 collection, and vice versa. The vector store validates collection configuration at startup and returns an error if mismatched.
> - **Fallback Behavior**: If attempting keyword or hybrid retrieval without BM25 enabled, keyword retrieval will return an error, and hybrid retrieval will fall back to vector-only retrieval (warning logged if logger is configured).
> - **Configuration Consistency**: When connecting to existing collections, always use the same BM25 setting. If you indexed documents with `WithBM25(true)`, you must also use `WithBM25(true)` when creating new vector store instances on that collection.
