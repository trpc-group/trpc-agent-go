# TcVector (Tencent Cloud Vector Database)

> **Example Code**: [examples/knowledge/vectorstores/tcvector](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/tcvector)

TcVector is the Tencent Cloud vector database implementation, supporting both local and remote embedding modes.

## Embedding Modes

TcVector supports two embedding modes:

### 1. Local Embedding Mode (Default)

Use local embedder to compute vectors, then store to TcVector:

```go
import (
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// Local embedding mode
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
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
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// Remote embedding mode
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
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
// v0.4.0+ new collections (TCVector service supports JSON index)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    // ... other configuration
)
// All metadata fields can be queried via JSON index, no predefinition needed

// Optional: Build additional indexes for frequently queried fields to optimize performance
metadataKeys := source.GetAllMetadataKeys(sources)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // Optional: build additional indexes
    // ... other configuration
)

// Collections before v0.4.0 or TCVector service doesn't support JSON index
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // Required: predefine filter fields
    // ... other configuration
)
```

**Notes:**
- **v0.4.0+ new collections**: Automatically create metadata JSON index, all fields queryable
- **Older version collections**: Only support fields predefined in `WithFilterIndexFields`
