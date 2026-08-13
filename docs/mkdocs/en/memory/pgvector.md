# pgvector Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, vector similarity search with PostgreSQL + pgvector

```go
import memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithHost("localhost"),
    memorypgvector.WithPort(5432),
    memorypgvector.WithUser("postgres"),
    memorypgvector.WithPassword("password"),
    memorypgvector.WithDatabase("dbname"),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Connection parameters
- `WithSSLMode(mode)`: SSL mode (default "disable")
- `WithPostgresInstance(name)`: Use pre-registered PostgreSQL instance
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithSchema(schema)`: Specify database schema (default is public)
- `WithIndexDimension(dim)`: Vector dimension (default 1536)
- `WithMaxResults(limit)`: Max search results (default 10)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to PostgreSQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)
- `WithHNSWIndexParams(params)`: HNSW index parameters for vector search

**Note**: Direct connection parameters take priority over `WithPostgresInstance`. Requires pgvector extension to be installed in PostgreSQL.

**Table schema** (auto-created):

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Indexes for performance
CREATE INDEX ON memories(app_name, user_id);
CREATE INDEX ON memories(updated_at DESC);
CREATE INDEX ON memories(deleted_at);
CREATE INDEX ON memories USING hnsw (embedding vector_cosine_ops);
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer pgvectorService.Close()
```
