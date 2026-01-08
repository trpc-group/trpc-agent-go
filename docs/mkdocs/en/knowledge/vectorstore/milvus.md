# Milvus

> **Example Code**: [examples/knowledge/vectorstores/milvus](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/milvus)

[Milvus](https://milvus.io/) is a high-performance vector database designed for billion-scale vector search scenarios.

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectormilvus "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
)

milvusVS, err := vectormilvus.New(ctx,
    vectormilvus.WithAddress("localhost:19530"),
    vectormilvus.WithCollectionName("my_documents"),
    vectormilvus.WithDimension(1536),
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(milvusVS),
    knowledge.WithEmbedder(embedder),
)
```

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithAddress(address)` | Milvus server address | - |
| `WithUsername(username)` | Username | - |
| `WithPassword(password)` | Password | - |
| `WithDBName(dbName)` | Database name | - |
| `WithAPIKey(apiKey)` | API Key authentication | - |
| `WithDialOptions(opts...)` | gRPC connection options | `Timeout=5s` |

### Collection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithCollectionName(name)` | Collection name | `"trpc_agent_documents"` |
| `WithDimension(dim)` | Vector dimension | `1536` |
| `WithMetricType(type)` | Similarity metric type (IP/L2/COSINE) | `entity.IP` |

### Index Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithHNSWParams(m, efConstruction)` | HNSW index parameters | `M=16, EfConstruction=128` |

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(max)` | Default number of search results | `10` |
| `WithReranker(reranker)` | Set reranker | - |
| `WithDocBuilder(builder)` | Custom document builder method | Default builder |

### Field Mapping (Advanced)

| Option | Description | Default |
|--------|-------------|---------|
| `WithIDField(field)` | ID field name | `"id"` |
| `WithNameField(field)` | Name field name | `"name"` |
| `WithContentField(field)` | Content field name | `"content"` |
| `WithVectorField(field)` | Vector field name | `"vector"` |
| `WithMetadataField(field)` | Metadata field name | `"metadata"` |
| `WithCreatedAtField(field)` | Created time field name | `"created_at"` |
| `WithUpdatedAtField(field)` | Updated time field name | `"updated_at"` |

## Similarity Metric Types

Milvus supports multiple similarity metric types:

```go
import "github.com/milvus-io/milvus/client/v2/entity"

// Inner Product - default, higher scores mean more similar
vectormilvus.WithMetricType(entity.IP)

// Euclidean Distance (L2) - lower scores mean more similar
vectormilvus.WithMetricType(entity.L2)

// Cosine Similarity
vectormilvus.WithMetricType(entity.COSINE)
```

## Usage Example

```go
import (
    vectormilvus "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
    "github.com/milvus-io/milvus/client/v2/entity"
)

milvusVS, err := vectormilvus.New(ctx,
    vectormilvus.WithAddress("localhost:19530"),
    vectormilvus.WithUsername("root"),
    vectormilvus.WithPassword("Milvus"),
    vectormilvus.WithCollectionName("knowledge_base"),
    vectormilvus.WithDimension(1536),
    vectormilvus.WithMetricType(entity.COSINE),
    vectormilvus.WithHNSWParams(32, 256),  // Higher recall
    vectormilvus.WithMaxResults(20),
)
```
