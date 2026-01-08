# TcVector (Tencent Cloud Vector Database)

> **Example Code**: [examples/knowledge/vectorstores/tcvector](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/tcvector)

TcVector is the Tencent Cloud vector database implementation, supporting both local and remote embedding modes.

## Embedding Modes

TcVector supports two embedding modes:

### 1. Local Embedding Mode (Default)

Use local embedder to compute vectors, then store to TcVector:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// Local embedding mode
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    vectortcvector.WithFilterAll(true), // Recommended: automatically index all metadata fields
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(tcVS),
    knowledge.WithEmbedder(embedder), // Requires local embedder configuration
)
```

### 2. Remote Embedding Mode

Use TcVector cloud embedding computation, no local embedder needed, saves resources:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// Remote embedding mode
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    vectortcvector.WithFilterAll(true), // Recommended: automatically index all metadata fields
    // Enable remote embedding computation
    vectortcvector.WithRemoteEmbeddingModel("bge-base-zh"),
    // Enable TSVector for hybrid retrieval if needed
    vectortcvector.WithEnableTSVector(true),
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(tcVS),
    // Note: When using remote embedding, no embedder configuration needed
    // knowledge.WithEmbedder(embedder), // Not needed
)
```

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithURL(url)` | TcVector service endpoint | - |
| `WithUsername(username)` | Username | - |
| `WithPassword(password)` | Password | - |
| `WithDatabase(database)` | Database name | `"trpc-agent-go"` |
| `WithCollection(collection)` | Collection name | `"documents"` |
| `WithTCVectorInstance(name)` | Use registered TcVector instance (lower priority than direct connection config) | - |

### Vector Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithIndexDimension(dim)` | Vector dimension (must match embedding model) | `1536` |
| `WithRemoteEmbeddingModel(model)` | Remote embedding model name (e.g., bge-base-zh) | - |
| `WithEnableTSVector(enabled)` | Enable hybrid retrieval | `true` |
| `WithHybridSearchWeights(vector, text)` | Hybrid retrieval weights (vector/text) | `0.7, 0.3` |
| `WithLanguage(lang)` | Text tokenization language (zh/en) | `"en"` |

### Index Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithReplicas(n)` | Number of replicas | `0` |
| `WithSharding(n)` | Number of shards | `1` |
| `WithFilterIndexFields(fields)` | Build filter indexes for specified fields | - |
| `WithFilterAll(enabled)` | Enable full-field filtering (skip index creation) | `false` |

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Default number of search results | `10` |
| `WithDocBuilder(builder)` | Custom document builder method | Default builder |

### Field Mapping (Advanced)

| Option | Description | Default |
|--------|-------------|---------|
| `WithIDField(field)` | ID field name | `"id"` |
| `WithNameField(field)` | Name field name | `"name"` |
| `WithContentField(field)` | Content field name | `"content"` |
| `WithEmbeddingField(field)` | Vector field name | `"vector"` |
| `WithMetadataField(field)` | Metadata field name | `"metadata"` |
| `WithCreatedAtField(field)` | Created time field name | `"created_at"` |
| `WithUpdatedAtField(field)` | Updated time field name | `"updated_at"` |
| `WithSparseVectorField(field)` | Sparse vector field name | `"sparse_vector"` |

## Filter Support

TcVector filter support:

- ✅ Supports all metadata filtering
- ✅ v0.4.0+ new collections automatically support JSON index (requires TCVector service support)
- ⚡ Optional: Use `WithFilterIndexFields` to build additional indexes for frequently queried fields

```go
// Recommended configuration (suitable for most scenarios)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterAll(true), // Recommended: automatically index all metadata fields, no need to manually manage indexes
    // ... other configuration
)

// Optional: Build additional indexes for frequently queried fields to optimize performance (use with WithFilterAll(true))
metadataKeys := source.GetAllMetadataKeys(sources)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterAll(true),
    vectortcvector.WithFilterIndexFields(metadataKeys), // Optional: build additional inverted indexes for faster queries
    // ... other configuration
)
```

**Notes:**
- **WithFilterAll(true)** (Recommended): Automatically create JSON index for `metadata` fields, making all metadata fields filterable without pre-defining schema.
- **WithFilterIndexFields** (Optional): Create additional inverted indexes for specific high-frequency query fields to further improve filtering performance on large datasets.
