# PGVector (PostgreSQL + pgvector)

> **Example Code**: [examples/knowledge/vectorstores/postgres](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/postgres)

PGVector is a vector store implementation based on PostgreSQL + pgvector extension, supporting hybrid retrieval (vector similarity + text relevance).

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://postgres:your-password@127.0.0.1:5432/your-database?sslmode=disable"),
    // Set index dimension based on embedding model (text-embedding-3-small is 1536)
    vectorpgvector.WithIndexDimension(1536),
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(pgVS),
    knowledge.WithEmbedder(embedder), // Requires local embedder configuration
)
```

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithPGVectorClientDSN(dsn)` | PostgreSQL connection string (highest priority) | - |
| `WithHost(host)` | Database host address | `"localhost"` |
| `WithPort(port)` | Database port | `5432` |
| `WithUser(user)` | Database username | - |
| `WithPassword(password)` | Database password | - |
| `WithDatabase(database)` | Database name | `"trpc_agent_go"` |
| `WithTable(table)` | Table name | `"documents"` |
| `WithSSLMode(mode)` | SSL mode | `"disable"` |
| `WithPostgresInstance(name)` | Use registered PostgreSQL instance (lower priority than direct connection config) | - |

### Vector Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithIndexDimension(dim)` | Vector dimension (must match embedding model) | `1536` |
| `WithVectorIndexType(type)` | Vector index type (`VectorIndexHNSW` / `VectorIndexIVFFlat`) | `VectorIndexHNSW` |
| `WithHNSWIndexParams(params)` | HNSW index parameters (M, EfConstruction) | `M=16, EfConstruction=64` |
| `WithIVFFlatIndexParams(params)` | IVFFlat index parameters (Lists) | `Lists=100` |

### Hybrid Retrieval Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithEnableTSVector(enabled)` | Enable text search vector | `true` |
| `WithHybridSearchWeights(vector, text)` | Hybrid retrieval weights (vector/text) | `0.7, 0.3` |
| `WithLanguageExtension(lang)` | Text tokenization language extension (e.g., zhparser/jieba) | `"english"` |

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Default number of search results | `10` |
| `WithDocBuilder(builder)` | Custom document builder method | Default builder |
| `WithExtraOptions(opts...)` | Inject custom PostgreSQL ClientBuilder config, not needed by default | - |

### Field Mapping (Advanced)

| Option | Description | Default |
|--------|-------------|---------|
| `WithIDField(field)` | ID field name | `"id"` |
| `WithNameField(field)` | Name field name | `"name"` |
| `WithContentField(field)` | Content field name | `"content"` |
| `WithEmbeddingField(field)` | Vector field name | `"embedding"` |
| `WithMetadataField(field)` | Metadata field name | `"metadata"` |
| `WithCreatedAtField(field)` | Created time field name | `"created_at"` |
| `WithUpdatedAtField(field)` | Updated time field name | `"updated_at"` |

## Hybrid Retrieval

PGVector supports hybrid retrieval, combining vector similarity search and full-text search:

```go
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN(dsn),
    vectorpgvector.WithIndexDimension(1536),
    vectorpgvector.WithEnableTSVector(true),           // Enable full-text search
    vectorpgvector.WithHybridSearchWeights(0.7, 0.3),  // 70% vector + 30% text
)
```
