# PostgreSQL Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, advanced JSONB features

Set `POSTGRES_DSN` through environment or secret management. A production DSN
should validate the server certificate and use a host name covered by it:

```text
postgres://<user>:<password>@db.example.com:5432/dbname?sslmode=verify-full&sslrootcert=<trusted-ca-path>
```

```go
import (
    "os"

    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresClientDSN(os.Getenv("POSTGRES_DSN")),
    memorypostgres.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
```

**Configuration options**:

- `WithPostgresClientDSN(dsn)`: Recommended connection form; has the highest priority
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Alternative field-based connection parameters
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

**Note**: A DSN takes priority over the field-based connection parameters. Both
direct forms take priority over `WithPostgresInstance`. The default SSL mode is
`disable`; use it only for trusted local development, not production.

**Registered instance example**:

```go
import (
    "os"

    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
    storagepostgres "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

storagepostgres.RegisterPostgresInstance(
    "my-postgres",
    storagepostgres.WithClientConnString(os.Getenv("POSTGRES_DSN")),
)
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresInstance("my-postgres"),
)
if err != nil {
    panic(err)
}
```

**Default table schema** (auto-created in `public.memories`):

`WithSchema` and `WithTableName` replace the schema and table identifiers in
this DDL and its index statements.

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
