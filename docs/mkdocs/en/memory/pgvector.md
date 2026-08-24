# pgvector Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, vector similarity search with PostgreSQL + pgvector

Set `PGVECTOR_DSN` through environment or secret management. A production DSN
should validate the server certificate and use a host name covered by it:

```text
postgres://<user>:<password>@db.example.com:5432/dbname?sslmode=verify-full&sslrootcert=<trusted-ca-path>
```

```go
import (
    "os"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
)

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithPGVectorClientDSN(os.Getenv("PGVECTOR_DSN")),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
```

**Configuration options**:

- `WithPGVectorClientDSN(dsn)`: Recommended connection form; has the highest priority
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Alternative field-based connection parameters
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

**Note**: A DSN takes priority over the field-based connection parameters. Both
direct forms take priority over `WithPostgresInstance`. The default SSL mode is
`disable`; use it only for trusted local development, not production. The
pgvector extension must be installed in PostgreSQL.

**Default initialized schema**:

The service enables the `vector` extension and initializes `public.memories`
with vector, episodic, and full-text fields. `WithTableName`, `WithSchema`, and
`WithIndexDimension` replace `memories`, `public`, and `1536`. It also creates
indexes for app/user, update time, deletion time, event time, kind,
participants (GIN), embedding (HNSW), and `search_vector` (GIN), plus a trigger
that maintains `search_vector` from `memory_content`.

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE public.memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    memory_kind TEXT NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP NULL,
    participants TEXT[],
    location TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    search_vector tsvector
);
```

`WithSkipDBInit(true)` skips the extension, table, indexes, trigger function,
trigger, and full-text backfill. Provision all of them before starting the
service; use
[`memory/pgvector/init.go`](https://github.com/trpc-group/trpc-agent-go/blob/main/memory/pgvector/init.go)
as the authoritative DDL, including custom HNSW parameters.

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer pgvectorService.Close()
```
