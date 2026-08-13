# MySQL Vector (mysqlvec) Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, vector similarity search with MySQL + native VECTOR type

MySQL Vector stores memories in MySQL with embedding vectors for semantic similarity
search. It detects native `VECTOR` support at runtime and otherwise falls back
to `BLOB` storage with Go-side cosine similarity. Use a currently supported
MySQL 9.x release for native-vector production deployments.

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
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

`WithMySQLClientDSN` takes priority over `WithMySQLInstance` when both are set.

**Note**: Requires MySQL 5.7.8+ for the JSON column type. The service probes
native `VECTOR` support and falls back to BLOB + Go-side cosine similarity when
the probe fails. No additional vector library is required.

**Default table schema** (auto-created when native `VECTOR` is available):

`WithTableName` replaces `memories`, and `WithIndexDimension` replaces `1536`.
On the fallback path, `embedding VECTOR(1536) NOT NULL` becomes
`embedding BLOB NOT NULL`; the remaining schema is the same.

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536) NOT NULL,
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    FULLTEXT INDEX idx_fulltext (memory_content),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at),
    INDEX idx_event_time (event_time DESC),
    INDEX idx_kind (app_name, user_id, memory_kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer mysqlvecService.Close()
```
