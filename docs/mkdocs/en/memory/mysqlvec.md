# MySQL Vector (mysqlvec) Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, vector similarity search with MySQL + native VECTOR type

MySQL Vector stores memories in MySQL with embedding vectors for semantic similarity
search. It uses MySQL 9.0+ native `VECTOR` type when available, and automatically
falls back to `BLOB` storage with Go-side cosine similarity for older versions (8.x).

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithMySQLClientDSN(dsn)`: MySQL DSN connection string (recommended, requires `parseTime=true`)
- `WithMySQLInstance(name)`: Use pre-registered MySQL instance
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithIndexDimension(dim)`: Vector dimension (default 1536)
- `WithMaxResults(limit)`: Max search results (default 15)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to MySQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**Note**: Requires MySQL 5.7.8+ (for JSON column type). Uses native VECTOR on MySQL 9.0+; falls back to BLOB + Go-side cosine similarity on MySQL 5.7/8.x. No additional vector library required.

**Table schema** (auto-created, MySQL 9.0+):

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536),
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer mysqlvecService.Close()
```
