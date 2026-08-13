# Storage Backends

## In-Memory Storage

**Use case**: Development, testing, rapid prototyping

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**Configuration options**:

- `WithMemoryLimit(limit int)`: Set memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool implementation
- `WithToolEnabled(toolName, enabled)`: Enable/disable specific tool

**Features**: Zero config, high performance, no persistence

## SQLite Storage

**Use case**: Local persistence, single-node deployments, demos

SQLite stores data in a single file. It is useful when you want persistence
without operating MySQL/PostgreSQL/Redis.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
)

db, err := sql.Open("sqlite3", "file:memories.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- `NewService` owns the `*sql.DB` and closes it in `Close()`.

## SQLiteVec (sqlite-vec) Storage

**Use case**: Local persistence + semantic memory search on a single node

SQLiteVec stores memories in a SQLite file and uses `sqlite-vec` to do
vector similarity search (semantic search). Compared to the plain SQLite
backend, it requires an **embedder** to generate embeddings.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

emb := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithSoftDelete(true),
    memorysqlitevec.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithIndexDimension(dim)`: Vector dimension (default is embedder dimension)
- `WithMaxResults(limit)`: Max search results (default 10)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`,
  `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- The `sqlite-vec` extension is compiled and registered in-process via Go
  bindings (no external `.so/.dylib` download at runtime).

## Redis Storage

**Use case**: Production, high concurrency, distributed deployment

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
```

**Configuration options**:

- `WithRedisClientURL(url)`: Redis connection URL (recommended)
- `WithRedisInstance(name)`: Use pre-registered Redis instance
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithKeyPrefix(prefix)`: Set a prefix for all Redis keys. When set, every key is prefixed with `prefix:`. For example, if `prefix` is `"myapp"`, the key `mem:{app:user}` becomes `myapp:mem:{app:user}`. Default is empty (no prefix). This is useful for sharing a single Redis instance across multiple environments or services
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to Redis client

**Note**: `WithRedisClientURL` takes priority over `WithRedisInstance`

**Redis ACL requirement**: `UpdateMemory` uses a server-side Lua script to
atomically validate and rotate memory IDs. ACL users must be allowed to run
`EVALSHA` and `EVAL` (`EVAL` is required when the script is not yet cached), in
addition to the script's `HEXISTS`, `HSET`, and `HDEL` commands and access to
the configured memory-key pattern. Do not remove `EVAL` after warm-up because
the Redis script cache can be cleared by a restart or `SCRIPT FLUSH`.

**Key prefix example**:

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
```

## MySQL Storage

**Use case**: Production, ACID guarantees, complex queries

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithMySQLClientDSN(dsn)`: MySQL DSN connection string (recommended, requires `parseTime=true`)
- `WithMySQLInstance(name)`: Use pre-registered MySQL instance
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to MySQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**DSN example**:

```
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**Table schema** (auto-created):

```sql
CREATE TABLE memories (
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_name, user_id, memory_id),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer mysqlService.Close()
```

## MySQL Vector (mysqlvec) Storage

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

## PostgreSQL Storage

**Use case**: Production, advanced JSONB features

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithHost("localhost"),
    memorypostgres.WithPort(5432),
    memorypostgres.WithUser("postgres"),
    memorypostgres.WithPassword("password"),
    memorypostgres.WithDatabase("dbname"),
    memorypostgres.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Connection parameters
- `WithSSLMode(mode)`: SSL mode (default "disable")
- `WithPostgresInstance(name)`: Use pre-registered PostgreSQL instance
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithSchema(schema)`: Specify database schema (default is public)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to PostgreSQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**Note**: Direct connection parameters take priority over `WithPostgresInstance`

**Table schema** (auto-created):

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at);
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer postgresService.Close()
```

## pgvector Storage

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

## ChromaDB Storage

**Use case**: Self-hosted ChromaDB or Chroma Cloud with cosine semantic and
hybrid memory search

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorychromadb "trpc.group/trpc-go/trpc-agent-go/memory/chromadb"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

chromaService, err := memorychromadb.NewService(
    memorychromadb.WithBaseURL("http://localhost:8000"),
    memorychromadb.WithCollectionName("memories"),
    memorychromadb.WithEmbedder(embedder),
    memorychromadb.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer chromaService.Close()
```

This is a client-server REST adapter, not an embedded Chroma runtime. Start a
Chroma server separately, or point `WithBaseURL` at a remote deployment or
Chroma Cloud. Embeddings are generated by the configured tRPC-Agent-Go
`embedder.Embedder`; the adapter does not install or invoke a Chroma
server-side embedding function.

For Chroma Cloud, add `WithAPIKey`; it is sent as `X-Chroma-Token`. The service
resolves the unique tenant and database from the identity endpoint unless they
are set explicitly. Bearer and custom-header authentication are available
through `WithBearerToken` and `WithHTTPHeaders`, primarily for proxies and
custom gateways. Custom authentication headers require explicit tenant and
database values. Any authenticated or custom-header connection to a
non-loopback host must use HTTPS.

**Configuration options**:

- Connection: `WithBaseURL`, `WithAPIKey`, `WithBearerToken`,
  `WithHTTPHeaders`, `WithTenant`, `WithDatabase`, `WithHTTPClient`,
  `WithTimeout`
- Collection: `WithCollectionName`, `WithAutoCreateCollection`,
  `WithIndexDimension`, `WithEmbedder`
- Retrieval: `WithMaxResults`, `WithSimilarityThreshold`,
  `WithHybridCandidateLimit`
- Retention: `WithMemoryLimit`, `WithSoftDelete`
- Auto mode and tools use the same options as other memory backends.

The adapter uses ChromaDB REST API v2 directly and requires an existing HNSW
or SPANN index configured with `cosine`. Records are isolated by schema,
application, and user metadata inside one collection. The per-user memory
limit is serialized within one service instance; use a distributed lock or
sticky routing if multiple instances write the same user concurrently.

Keep these operational constraints in mind:

- Changing the embedding model requires a new collection or re-embedding every
  record, even when the old and new models have the same vector dimension.
- `EventTime` values and search time bounds must fit in signed 64-bit Unix
  nanoseconds, from 1677-09-21 through 2262-04-11 UTC. Values outside this
  range are rejected before a ChromaDB request is made.
- `WithHybridCandidateLimit` is the hard cap for the local keyword candidate
  scan; it is independent of `WithMemoryLimit`.
- Capacity checks, ID rotation, and paginated reads are best-effort across
  multiple service instances because Chroma does not expose transactions or a
  pagination snapshot token for this workflow.
- Chroma Cloud currently documents a 128-byte collection-name limit, up to 300
  query results, up to 300 records per write, and 10 concurrent reads and 10
  concurrent writes per collection. The adapter documents but does not
  silently clamp these service limits.

## Backend Comparison

| Feature           | InMemory  | SQLite            | SQLiteVec        | Redis            | MySQL      | MySQL Vec         | PostgreSQL        | pgvector      | ChromaDB       |
| ----------------- | --------- | ----------------- | ---------------- | ---------------- | ---------- | ----------------- | ----------------- | ------------- | -------------- |
| **Persistence**   | ❌        | ✅                | ✅               | ✅               | ✅         | ✅                | ✅                | ✅            | ✅             |
| **Distributed**   | ❌        | ❌                | ❌               | ✅               | ✅         | ✅                | ✅                | ✅            | ✅             |
| **Transactions**  | ❌        | ✅ ACID           | ✅ ACID          | Partial          | ✅ ACID    | ✅ ACID           | ✅ ACID           | ✅ ACID       | Best effort    |
| **Queries**       | Simple    | SQL               | SQL + Vector     | Medium           | SQL        | SQL + Vector      | SQL               | SQL + Vector  | Vector + Local |
| **JSON**          | ❌        | Basic             | Basic            | Basic            | JSON       | JSON              | JSONB             | JSONB         | Metadata       |
| **Performance**   | Very High | Med-High          | Med-High         | High             | Med-High   | Med-High          | Med-High          | Med-High      | High           |
| **Configuration** | Zero      | Simple            | Medium           | Simple           | Medium     | Medium            | Medium            | Medium        | Medium         |
| **Soft Delete**   | ❌        | ✅                | ✅               | ❌               | ✅         | ✅                | ✅                | ✅            | ✅             |
| **Use Case**      | Dev/Test  | Local Persistence | Local Vector     | High Concurrency | Enterprise | MySQL Vector Search | Advanced Features | Vector Search | Vector Service |

**Selection guide**:

```
Development/Testing → InMemory (zero config, fast)
Local Persistence → SQLite (single-file DB, easy setup)
Local Vector Search → SQLiteVec (single-file DB + embeddings)
High Concurrency → Redis (memory-level performance)
ACID Requirements → MySQL/PostgreSQL (transaction guarantees)
Complex JSON → PostgreSQL (JSONB indexing and queries)
MySQL Vector Search → mysqlvec (similarity search on MySQL 9.0+)
Vector Search → pgvector (similarity search with embeddings)
Managed Vector Service → ChromaDB (REST-based cosine and hybrid search)
Audit Trail → MySQL/MySQL Vec/PostgreSQL/pgvector/SQLite/SQLiteVec/ChromaDB (soft delete support)
```

**Register PostgreSQL Instance (Optional):**

```go
import (
    storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// Register PostgreSQL instance
storage.RegisterPostgresInstance("my-postgres",
    storage.WithClientConnString("postgres://user:password@localhost:5432/dbname"),
)

// Use registered instance
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresInstance("my-postgres"),
)
```

## Storage Backend Comparison

| Feature                  | In-Memory | SQLite     | SQLiteVec    | Redis      | MySQL          | MySQL Vec     | PostgreSQL     | pgvector      | ChromaDB       |
| ------------------------ | --------- | ---------- | ------------ | ---------- | -------------- | ------------- | -------------- | ------------- | -------------- |
| Data Persistence         | ❌        | ✅         | ✅           | ✅         | ✅             | ✅            | ✅             | ✅            | ✅             |
| Distributed Support      | ❌        | ❌         | ❌           | ✅         | ✅             | ✅            | ✅             | ✅            | ✅             |
| Transaction Support      | ❌        | ✅ (ACID)  | ✅ (ACID)    | Partial    | ✅ (ACID)      | ✅ (ACID)     | ✅ (ACID)      | ✅ (ACID)     | Best effort    |
| Query Capability         | Simple    | SQL        | SQL + Vector | Medium     | Powerful (SQL) | SQL + Vector  | Powerful (SQL) | SQL + Vectors | Vector + Local |
| JSON Support             | ❌        | Basic      | Basic        | Partial    | ✅ (JSON)      | ✅ (JSON)     | ✅ (JSONB)     | ✅ (JSONB)    | Metadata       |
| Performance              | Very High | Med-High   | Medium-High  | High       | Medium-High    | Medium-High   | Medium-High    | Medium-High   | High           |
| Configuration Complexity | Low       | Low        | Medium       | Medium     | Medium         | Medium        | Medium         | Medium        | Medium         |
| Use Case                 | Dev/Test  | Local Dev  | Local Vector | Production | Production     | MySQL Vector  | Production     | Vector Search | Vector Service |
| Monitoring Tools         | None      | None       | None         | Rich       | Very Rich      | Very Rich     | Very Rich      | Very Rich     | Chroma tooling |

**Selection Guide:**

- **Development/Testing**: Use in-memory storage for fast iteration
- **Local Development (Persistent)**: Use SQLite when you want persistence without operating an external database
- **Local Development (Vector Search)**: Use SQLiteVec when you want semantic search in a single-file SQLite DB
- **Production (High Performance)**: Use Redis storage for high concurrency scenarios
- **Production (Data Integrity)**: Use MySQL storage when ACID guarantees and complex queries are needed
- **Production (MySQL Vector Search)**: Use MySQL Vec for similarity search on MySQL 9.0+
- **Production (PostgreSQL)**: Use PostgreSQL storage when JSONB support and advanced PostgreSQL features are needed
- **Production (Vector Search)**: Use pgvector storage when similarity search with embeddings is needed
- **Production (Vector Service)**: Use ChromaDB for REST-based cosine and hybrid memory search
- **Hybrid Deployment**: Choose different storage backends based on different application scenarios
